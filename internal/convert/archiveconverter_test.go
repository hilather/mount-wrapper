package convert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

func intPtr(v int) *int { return &v }

func baseACCfg(t *testing.T, dir string, overrides func(*config.Config)) *config.Config {
	t.Helper()
	cfg, err := config.FromMap(map[string]any{
		"archiveconverter_enabled":    true,
		"archiveconverter_output_dir": filepath.Join(dir, "converted"),
		"archiveconverter_bin":        filepath.Join(dir, "fake-ac"),
		"archiveconverter_backend":    "native",
		"archiveconverter_mode":       "convert",
		"min_free_bytes":              0,
		"archiveconverter_overhead_bytes": 0,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if overrides != nil {
		overrides(cfg)
	}
	return cfg
}

func writeFakeBin(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func alwaysExec(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func TestIsSevenzPath(t *testing.T) {
	t.Parallel()
	if !convert.IsSevenzPath("/a/b.7z") || !convert.IsSevenzPath("A.7Z") {
		t.Fatal("expected sevenz")
	}
	if convert.IsSevenzPath("a.tar.gz") {
		t.Fatal("not sevenz")
	}
}

func TestShouldConvert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-ac")
	writeFakeBin(t, bin)
	src := filepath.Join(dir, "src", "a.7z")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("7z"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := baseACCfg(t, dir, func(c *config.Config) {
		c.ArchiveconverterBin = bin
	})
	opts := convert.ResolveOptions{
		IsExecutable: alwaysExec,
		SearchPathDisabled: true,
	}

	if !convert.ShouldConvert(cfg, src, true, opts) {
		t.Fatal("expected should convert")
	}
	if convert.ShouldConvert(cfg, src, false, opts) {
		t.Fatal("needs_index=false must skip")
	}
	cfgOff := baseACCfg(t, dir, func(c *config.Config) {
		c.ArchiveconverterEnabled = false
		c.ArchiveconverterBin = bin
	})
	if convert.ShouldConvert(cfgOff, src, true, opts) {
		t.Fatal("disabled")
	}
	tar := filepath.Join(dir, "src", "a.tar")
	if err := os.WriteFile(tar, []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldConvert(cfg, tar, true, opts) {
		t.Fatal("non-7z")
	}
	// Already under converted dir
	converted := filepath.Join(dir, "converted", "id.7z")
	if err := os.MkdirAll(filepath.Dir(converted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(converted, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldConvert(cfg, converted, true, opts) {
		t.Fatal("already converted path")
	}
	if !convert.IsConvertedPath(cfg, converted) {
		t.Fatal("IsConvertedPath")
	}
	if !convert.ArchiveconverterAvailable(cfg, opts) {
		t.Fatal("available")
	}
}

func TestBuildConvertCmd_matrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-ac")
	writeFakeBin(t, bin)

	t.Run("native convert full knobs", func(t *testing.T) {
		cfg := baseACCfg(t, dir, func(c *config.Config) {
			c.ArchiveconverterBin = bin
			c.ArchiveconverterExcludeInner = []string{`\.tmp$`}
			c.ArchiveconverterExcludeOuter = []string{"^skip/"}
			c.ArchiveconverterRename = []string{"a=b"}
			c.ArchiveconverterBasenameMatch = true
			c.ArchiveconverterLevel = 3
			c.ArchiveconverterThreads = intPtr(2)
			c.ArchiveconverterVerify = true
			c.ArchiveconverterTempDir = "/tmp/ac"
			c.ArchiveconverterNestedConcurrency = intPtr(1)
			c.ArchiveconverterNestedSizeBudget = "1G"
			c.ArchiveconverterNativeLargeThreshold = 1048576
			c.ArchiveconverterExtraArgs = []string{"--quiet"}
		})
		cmd, err := convert.BuildConvertCmd(cfg, bin, filepath.Join(dir, "in.7z"), filepath.Join(dir, "out.7z"))
		if err != nil {
			t.Fatal(err)
		}
		if cmd[0] != bin || cmd[1] != "convert" {
			t.Fatalf("cmd head %v", cmd[:2])
		}
		assertPair(t, cmd, "-o", filepath.Join(dir, "out.7z"))
		assertPair(t, cmd, "--backend", "native")
		assertPair(t, cmd, "--native-pipeline", "parallel")
		assertPair(t, cmd, "--native-codec", "liblzma")
		assertPair(t, cmd, "--native-large-threshold", "1048576")
		assertPair(t, cmd, "--nested-concurrency", "1")
		assertPair(t, cmd, "--nested-size-budget", "1G")
		assertPair(t, cmd, "--level", "3")
		assertPair(t, cmd, "--threads", "2")
		assertContains(t, cmd, "--verify")
		assertPair(t, cmd, "--exclude-inner", `\.tmp$`)
		assertPair(t, cmd, "--exclude-outer", "^skip/")
		assertPair(t, cmd, "--rename", "a=b")
		assertContains(t, cmd, "--basename-match")
		assertPair(t, cmd, "--temp-dir", "/tmp/ac")
		assertContains(t, cmd, "--quiet")
	})

	t.Run("cli backend omits native flags", func(t *testing.T) {
		cfg := baseACCfg(t, dir, func(c *config.Config) {
			c.ArchiveconverterBin = bin
			c.ArchiveconverterBackend = config.ArchiveconverterBackendCLI
		})
		cmd, err := convert.BuildConvertCmd(cfg, bin, "in.7z", "out.7z")
		if err != nil {
			t.Fatal(err)
		}
		assertPair(t, cmd, "--backend", "cli")
		if contains(cmd, "--native-pipeline") || contains(cmd, "--native-codec") {
			t.Fatalf("native flags present: %v", cmd)
		}
	})

	t.Run("convert-single uses --exclude", func(t *testing.T) {
		cfg := baseACCfg(t, dir, func(c *config.Config) {
			c.ArchiveconverterBin = bin
			c.ArchiveconverterMode = config.ArchiveconverterModeConvertSingle
			c.ArchiveconverterExcludeInner = []string{"x"}
			c.ArchiveconverterNestedConcurrency = intPtr(2)
		})
		cmd, err := convert.BuildConvertCmd(cfg, bin, "in.7z", "out.7z")
		if err != nil {
			t.Fatal(err)
		}
		if cmd[1] != "convert-single" {
			t.Fatalf("mode=%s", cmd[1])
		}
		assertPair(t, cmd, "--exclude", "x")
		if contains(cmd, "--exclude-inner") {
			t.Fatal("unexpected --exclude-inner")
		}
		if contains(cmd, "--nested-concurrency") {
			t.Fatal("nested concurrency only for convert mode")
		}
	})

	t.Run("empty bin error", func(t *testing.T) {
		cfg := baseACCfg(t, dir, nil)
		_, err := convert.BuildConvertCmd(cfg, "", "a", "b")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResolveArchiveconverterBin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "ac")
	writeFakeBin(t, p)
	got := convert.ResolveArchiveconverterBin(p, convert.ResolveOptions{
		IsExecutable: alwaysExec,
		SearchPathDisabled: true,
	})
	if got != p {
		t.Fatalf("got %q", got)
	}
	// Explicit missing path returned as-is
	missing := filepath.Join(dir, "nope")
	got = convert.ResolveArchiveconverterBin(missing, convert.ResolveOptions{
		IsExecutable: alwaysExec,
		SearchPathDisabled: true,
	})
	if got != missing {
		t.Fatalf("got %q", got)
	}
	// Auto with sibling
	sib := filepath.Join(dir, "sibling-ac")
	writeFakeBin(t, sib)
	got = convert.ResolveArchiveconverterBin("", convert.ResolveOptions{
		IsExecutable:       alwaysExec,
		SearchPathDisabled: true,
		SiblingRelease:     sib,
	})
	if got != sib {
		t.Fatalf("sibling got %q", got)
	}
}

func TestExistingConvertedPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := baseACCfg(t, dir, nil)
	id := "abc-123"
	if convert.ExistingConvertedPath(cfg, id) != "" {
		t.Fatal("expected empty")
	}
	dest := convert.ConvertedFilePath(cfg, id)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("converted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := convert.ExistingConvertedPath(cfg, id); got != dest {
		t.Fatalf("got %q want %q", got, dest)
	}
}

func TestShouldPreconvert_archiveconverter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-ac")
	writeFakeBin(t, bin)
	cfg := baseACCfg(t, dir, func(c *config.Config) {
		c.ArchiveconverterBin = bin
	})
	opts := convert.ResolveOptions{IsExecutable: alwaysExec, SearchPathDisabled: true}
	src := filepath.Join(dir, "src", "done.7z")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("7z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !convert.ShouldPreconvert(cfg, src, opts, nil) {
		t.Fatal("expected preconvert")
	}
	meta := convert.BuildConvertMetadata(3, 3, "archiveconverter", nil)
	if _, err := convert.WriteConvertMetadata(src, meta); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldPreconvert(cfg, src, opts, nil) {
		t.Fatal("metadata should skip")
	}
}

func assertContains(t *testing.T, cmd []string, want string) {
	t.Helper()
	if !contains(cmd, want) {
		t.Fatalf("missing %q in %v", want, cmd)
	}
}

func assertPair(t *testing.T, cmd []string, flag, value string) {
	t.Helper()
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == flag && cmd[i+1] == value {
			return
		}
	}
	t.Fatalf("missing pair %s %s in %v", flag, value, cmd)
}

func contains(cmd []string, want string) bool {
	for _, a := range cmd {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildConvertCmd_threadsOmittedWhenNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := baseACCfg(t, dir, func(c *config.Config) {
		c.ArchiveconverterThreads = nil
	})
	cmd, err := convert.BuildConvertCmd(cfg, "/bin/ac", "in", "out")
	if err != nil {
		t.Fatal(err)
	}
	if contains(cmd, "--threads") {
		t.Fatalf("threads should be omitted: %v", cmd)
	}
	// sanity: no accidental join of empty flags
	joined := strings.Join(cmd, " ")
	if strings.Contains(joined, "--threads  ") {
		t.Fatal(joined)
	}
}
