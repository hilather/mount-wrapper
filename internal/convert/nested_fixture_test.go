package convert_test

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

// nested7zDir returns absolute path to testdata/nested7z (repo root relative to this file).
func nested7zDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/convert/nested_fixture_test.go → repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	dir := filepath.Join(root, "testdata", "nested7z")
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		t.Fatalf("testdata/nested7z missing at %s: %v", dir, err)
	}
	return dir
}

func readNestedListing(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(nested7zDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lookPath7z returns an executable 7z binary or empty string.
func lookPath7z() string {
	for _, name := range []string{"7z", "7zz"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return p
		}
	}
	return ""
}

func require7z(t *testing.T) string {
	t.Helper()
	bin := lookPath7z()
	if bin == "" {
		t.Skip("7z not on PATH (default make test must pass without 7z)")
	}
	return bin
}

func TestParse7zListNeedsFlatten_FixtureListings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string
		want bool
	}{
		{"SUP-36264-nested-mini.l-slt.txt", true},  // nested *.7z members
		{"nested-multi-mini.l-slt.txt", true},      // multi inner *.7z
		{"solid-mini.l-slt.txt", true},             // Solid = +
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			list := readNestedListing(t, tc.file)
			got := convert.Parse7zListNeedsFlatten(list)
			if got != tc.want {
				t.Fatalf("Parse7zListNeedsFlatten(%s)=%v want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestParse7zListNeedsFlatten_FixtureNestedMembers(t *testing.T) {
	t.Parallel()
	list := readNestedListing(t, "SUP-36264-nested-mini.l-slt.txt")
	// Sanity: listing names match manifest / upstream fixture.
	for _, name := range []string{
		"algosec-support-zip--CM-Primary--10.86.44.46--2026-07-14--08-44-03.7z",
		"algosec-support-zip--CM-Secondary--10.86.44.45--2026-07-14--08-47-35.7z",
		"collect-support-zip.log",
	} {
		if !strings.Contains(list, name) {
			t.Fatalf("listing missing %q", name)
		}
	}
	// Outer is non-solid; nested detection alone must drive true.
	if strings.Contains(list, "Solid = +") {
		t.Fatal("mini fixture listing should be non-solid outer")
	}
	if !convert.Parse7zListNeedsFlatten(list) {
		t.Fatal("expected nested members → needs flatten")
	}
}

func TestProbe7zNeedsFlatten_CommittedMiniFixture(t *testing.T) {
	// Not parallel: spawns 7z; skip when missing so default CI stays green.
	bin := require7z(t)
	archive := filepath.Join(nested7zDir(t), "SUP-36264-nested-mini.7z")
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
	if !convert.Probe7zNeedsFlatten(bin, archive, nil) {
		t.Fatal("real mini nested 7z should need flatten")
	}

	// Offline listing and live probe must agree.
	list := readNestedListing(t, "SUP-36264-nested-mini.l-slt.txt")
	if !convert.Parse7zListNeedsFlatten(list) {
		t.Fatal("captured listing must also parse as needs-flatten")
	}
}

func TestProbe7zNeedsFlatten_GeneratedMultiAndSolid(t *testing.T) {
	bin := require7z(t)
	dir := t.TempDir()

	// --- multi nested outer (relative names so Path= basenames are clean) ---
	mustWrite(t, filepath.Join(dir, "a.txt"), "a")
	mustWrite(t, filepath.Join(dir, "b.txt"), "b")
	mustWrite(t, filepath.Join(dir, "readme.txt"), "readme")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "inner-a.7z", "a.txt")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "inner-b.7z", "b.txt")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "nested-multi-mini.7z",
		"inner-a.7z", "inner-b.7z", "readme.txt")
	multi := filepath.Join(dir, "nested-multi-mini.7z")
	if !convert.Probe7zNeedsFlatten(bin, multi, nil) {
		t.Fatal("generated multi nested should need flatten")
	}

	// --- solid outer (no nested members) ---
	mustWrite(t, filepath.Join(dir, "s1.txt"), "s1")
	mustWrite(t, filepath.Join(dir, "s2.txt"), "s2")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=1", "-ms=on", "solid-mini.7z", "s1.txt", "s2.txt")
	solid := filepath.Join(dir, "solid-mini.7z")
	if !convert.Probe7zNeedsFlatten(bin, solid, nil) {
		t.Fatal("generated solid should need flatten")
	}

	// --- plain non-solid, no nested ---
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "plain.7z", "readme.txt")
	plain := filepath.Join(dir, "plain.7z")
	if convert.Probe7zNeedsFlatten(bin, plain, nil) {
		t.Fatal("plain non-solid non-nested should not need flatten")
	}
}

func TestCLIFlattenNeeded_GeneratedNested(t *testing.T) {
	bin := require7z(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "x.txt"), "x")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "inner.7z", "x.txt")
	mustRun7z(t, bin, dir, "a", "-t7z", "-mx=0", "-ms=off", "outer.7z", "inner.7z")
	outer := filepath.Join(dir, "outer.7z")

	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_7z_bin":      bin,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	opts := convert.ResolveOptions{SearchPathDisabled: true}
	fn := convert.CLIFlattenNeeded(cfg, opts, nil)
	if fn == nil {
		t.Fatal("nil fn")
	}
	if !fn(outer) {
		t.Fatal("expected nested outer true")
	}
	if !convert.ShouldFlattenConvert(cfg, outer, fn) {
		t.Fatal("ShouldFlattenConvert")
	}
}

func TestShouldRepackZip_EmbeddedSevenzMember(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Zip with embedded .7z member (bytes need not be a valid 7z — suffix scan only).
	zipPath := filepath.Join(dir, "bundle.zip")
	if err := writeZipMember(zipPath, "payloads/inner.7z", []byte("not-a-real-7z")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_zip_to_7z":   true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !convert.ZipHasEmbeddedArchives(zipPath) {
		t.Fatal("ZipHasEmbeddedArchives")
	}
	if !convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("ShouldRepackZip with embedded .7z")
	}
}

// TestShouldPreconvert_Matrix covers preconvert gates without real engines.
func TestShouldPreconvert_Matrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "plain.tar.gz")
	if err := os.WriteFile(tarPath, []byte("not-a-tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	sevenz := filepath.Join(dir, "archive.7z")
	if err := os.WriteFile(sevenz, []byte("not-a-7z"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dir, "with-nested.zip")
	if err := writeZipMember(zipPath, "inner/payload.tar.gz", []byte("x")); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":       true,
		"convert_7z_scope":          "flatten",
		"convert_zip_to_7z":         true,
		"archiveconverter_enabled":  false,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// No flatten probe → flatten path stays false; zip with embedded archive → true.
	if convert.ShouldPreconvert(cfg, tarPath, convert.ResolveOptions{}, nil) {
		t.Fatal("plain tar.gz should not preconvert without AC/zip/flatten")
	}
	if convert.ShouldPreconvert(cfg, sevenz, convert.ResolveOptions{}, nil) {
		t.Fatal("7z without probe/AC should not preconvert")
	}
	if !convert.ShouldPreconvert(cfg, zipPath, convert.ResolveOptions{}, nil) {
		t.Fatal("zip with embedded tar.gz should preconvert (repack)")
	}
	// Explicit flatten probe forces 7z path.
	probeTrue := func(string) bool { return true }
	if !convert.ShouldPreconvert(cfg, sevenz, convert.ResolveOptions{}, probeTrue) {
		t.Fatal("7z with FlattenNeeded true should preconvert")
	}
}

func mustWrite(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRun7z(t *testing.T, bin, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("7z %v: %v\n%s", args, err, out)
	}
}

func writeZipMember(dest, memberName string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	wr, err := w.Create(memberName)
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := wr.Write(payload); err != nil {
		_ = f.Close()
		return err
	}
	if err := w.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
