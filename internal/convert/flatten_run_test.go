package convert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestBuildFlattenCmds(t *testing.T) {
	t.Parallel()
	extract := convert.BuildFlattenExtractCmd("/bin/7z", "/data/a.7z", "/data/work", []string{"*.tmp", "skip/*"})
	if extract[0] != "/bin/7z" || extract[1] != "x" || extract[2] != "-y" {
		t.Fatalf("extract=%v", extract)
	}
	if extract[3] != "-o/data/work"+string(filepath.Separator) {
		t.Fatalf("out=%q", extract[3])
	}
	if extract[4] != "-xr!*.tmp" || extract[5] != "-xr!skip/*" {
		t.Fatalf("excludes=%v", extract)
	}
	if extract[6] != "/data/a.7z" {
		t.Fatalf("src=%q", extract[6])
	}

	create := convert.BuildFlattenCreateCmd("7z", "/data/a.7z.flatten.partial")
	want := []string{"7z", "a", "-t7z", "-ms=off", "-y", "/data/a.7z.flatten.partial", "*"}
	for i := range want {
		if create[i] != want[i] {
			t.Fatalf("i=%d got %q want %q", i, create[i], want[i])
		}
	}
}

func TestNestedFlattenPrefix(t *testing.T) {
	t.Parallel()
	if got := convert.NestedFlattenPrefix("inner/nested.7z", ""); got != "inner/nested" {
		t.Fatalf("got %q", got)
	}
	if got := convert.NestedFlattenPrefix("algosec-support-zip--foo.7z", "algosec-support-zip--"); got != "foo" {
		t.Fatalf("strip got %q", got)
	}
	if got := convert.StripInnerNamePrefix("prefix/rest", "prefix/"); got != "rest" {
		t.Fatalf("strip %q", got)
	}
}

func TestFlattenMinOKSize(t *testing.T) {
	t.Parallel()
	if convert.FlattenMinOKSize(100) != 200 {
		t.Fatal("small floor")
	}
	if convert.FlattenMinOKSize(1000) != 500 {
		t.Fatal("half")
	}
	// large: max(source/20, 64MiB) — 200MiB/20 is below the 64MiB floor
	big := int64(200 * 1024 * 1024)
	const floor64 = int64(64 * 1024 * 1024)
	if convert.FlattenMinOKSize(big) != floor64 {
		t.Fatalf("got %d want %d", convert.FlattenMinOKSize(big), floor64)
	}
	// above floor: 2GiB/20 = 100MiB
	huge := int64(2 * 1024 * 1024 * 1024)
	if convert.FlattenMinOKSize(huge) != huge/20 {
		t.Fatalf("huge got %d", convert.FlattenMinOKSize(huge))
	}
}

func TestRunFlattenConvert_Success(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	archive := filepath.Join(dir, "solid.7z")
	// Source large enough that min_ok = max(source/2, 200) is easy to satisfy.
	if err := os.WriteFile(archive, []byte(strings.Repeat("S", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}

	var steps []string
	run := func(bin string, args []string, cwd string) error {
		steps = append(steps, args[0])
		switch args[0] {
		case "x":
			var out string
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					out = strings.TrimPrefix(a, "-o")
				}
			}
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			// Outer extract: plant a nested 7z + a plain file.
			if strings.Contains(out, ".work") && !strings.Contains(filepath.Base(out), "nested") {
				if err := os.WriteFile(filepath.Join(out, "readme.txt"), []byte("hi"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(out, "nested.7z"), []byte("NESTED"), 0o644)
			}
			// Nested extract into out dir.
			return os.WriteFile(filepath.Join(out, "inner.txt"), []byte("inner"), 0o644)
		case "t":
			return nil // nested ok
		case "a":
			var partial string
			for _, a := range args {
				if strings.Contains(a, ".partial") {
					partial = a
				}
			}
			if cwd == "" {
				t.Fatal("create needs cwd")
			}
			// Must be >= FlattenMinOKSize(1000)=500
			return os.WriteFile(partial, []byte(strings.Repeat("F", 600)), 0o644)
		default:
			t.Fatalf("unexpected %v", args)
			return nil
		}
	}

	meta, err := convert.RunFlattenConvert(archive, convert.NonsolidFlattenParams{
		SevenZipBin: "7z",
		Run7z:       run,
	}, func(string) bool { return true })
	if err != nil {
		t.Fatalf("RunFlattenConvert: %v", err)
	}
	if meta == nil || meta.Method != convert.MethodFlattenCLI {
		t.Fatalf("meta=%v", meta)
	}
	st, err := os.Stat(archive)
	if err != nil || st.Size() != 600 {
		t.Fatalf("archive size=%v err=%v", st, err)
	}
	if convert.ReadConvertMetadata(archive) == nil {
		t.Fatal("metadata missing")
	}
	// partial/work cleaned
	if _, err := os.Stat(convert.FlattenPartialPath(archive)); !os.IsNotExist(err) {
		t.Fatal("partial should be gone")
	}
	if _, err := os.Stat(convert.FlattenWorkDir(convert.FlattenPartialPath(archive))); !os.IsNotExist(err) {
		t.Fatal("work should be gone")
	}
	// Expect extract, test, nested extract, create
	joined := strings.Join(steps, ",")
	if !strings.Contains(joined, "x") || !strings.Contains(joined, "a") {
		t.Fatalf("steps=%v", steps)
	}
}

func TestRunFlattenConvert_ProbeFalseSkips(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	run := func(string, []string, string) error {
		called = true
		return nil
	}
	meta, err := convert.RunFlattenConvert(archive, convert.NonsolidFlattenParams{
		Run7z: run,
	}, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Fatal("expected nil meta")
	}
	if called {
		t.Fatal("should not run 7z when probe false")
	}
}

func TestRunFlattenConvert_NilProbeSkips(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := convert.RunFlattenConvert(archive, convert.NonsolidFlattenParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Fatal("nil probe skips")
	}
}

func TestRunFlattenConvert_FailExtract(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte(strings.Repeat("x", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(_ string, args []string, _ string) error {
		return &convert.Error{Op: "run_7z", Msg: "7z failed: extract boom"}
	}
	_, err := convert.RunFlattenConvert(archive, convert.NonsolidFlattenParams{
		Run7z: run,
	}, func(string) bool { return true })
	if err == nil {
		t.Fatal("expected error")
	}
	// Source remains
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
}

func TestRunFlattenConvert_InsufficientSpace(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1, true
	})
	t.Cleanup(restore)

	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte(strings.Repeat("x", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := convert.RunFlattenConvert(archive, convert.NonsolidFlattenParams{
		Run7z: func(string, []string, string) error { return nil },
	}, func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("got %v", err)
	}
}

func TestShouldFlattenThenRun_WithConfig(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte(strings.Repeat("x", 800)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_7z_bin":      "7z",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	probe := func(string) bool { return true }
	if !convert.ShouldFlattenConvert(cfg, archive, probe) {
		t.Fatal("should flatten")
	}
	p := convert.FlattenParamsFromConfig(cfg, convert.ResolveOptions{SearchPathDisabled: true})
	p.Run7z = func(_ string, args []string, cwd string) error {
		if args[0] == "x" {
			var out string
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					out = strings.TrimPrefix(a, "-o")
				}
			}
			return os.MkdirAll(out, 0o755)
		}
		if args[0] == "a" {
			for _, a := range args {
				if strings.Contains(a, ".partial") {
					return os.WriteFile(a, []byte(strings.Repeat("Z", 500)), 0o644)
				}
			}
		}
		return nil
	}
	meta, err := convert.RunFlattenConvert(archive, p, probe)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("meta")
	}
}
