package convert_test

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

// nestedzipDir returns absolute path to testdata/nestedzip (repo root relative to this file).
func nestedzipDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/convert/nestedzip_fixture_test.go → repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	dir := filepath.Join(root, "testdata", "nestedzip")
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		t.Fatalf("testdata/nestedzip missing at %s: %v", dir, err)
	}
	return dir
}

func nestedzipFixturePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(nestedzipDir(t), "nested-with-archives.zip")
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("fixture missing: %s: %v", p, err)
	}
	return p
}

// copyNestedzipFixture copies the committed zip into dir (repack mutates/renames source).
func copyNestedzipFixture(t *testing.T, destDir string) string {
	t.Helper()
	src := nestedzipFixturePath(t)
	dest := filepath.Join(destDir, "nested-with-archives.zip")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dest
}

// TestZipHasEmbeddedArchives_CommittedFixture: offline — no 7z required.
func TestZipHasEmbeddedArchives_CommittedFixture(t *testing.T) {
	t.Parallel()
	zipPath := nestedzipFixturePath(t)
	if !convert.ZipHasEmbeddedArchives(zipPath) {
		t.Fatal("committed nestedzip fixture must report embedded archive members")
	}
}

// TestShouldRepackZip_CommittedFixture: offline predicate on real fixture bytes.
func TestShouldRepackZip_CommittedFixture(t *testing.T) {
	t.Parallel()
	// Copy so a stale sibling .7z / sidecar cannot poison the predicate.
	dir := t.TempDir()
	zipPath := copyNestedzipFixture(t, dir)

	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_zip_to_7z":   true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("ShouldRepackZip true for fixture with nested .7z / .tar.gz")
	}

	// Flag off → false.
	cfgOff, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_zip_to_7z":   false,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if convert.ShouldRepackZip(cfgOff, zipPath) {
		t.Fatal("ShouldRepackZip false when convert_zip_to_7z disabled")
	}
}

// TestRunZipRepack_Real7z_CommittedFixture runs real 7z extract+create on the
// committed zip. Skips when 7z is not on PATH so default make test stays green.
func TestRunZipRepack_Real7z_CommittedFixture(t *testing.T) {
	bin := require7z(t)
	dir := t.TempDir()
	zipPath := copyNestedzipFixture(t, dir)
	srcSt, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	// Also confirm ShouldRepackZip before the runner mutates the tree.
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_zip_to_7z":   true,
		"convert_7z_bin":      bin,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("ShouldRepackZip before repack")
	}

	dest, meta, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin:   bin,
		OverheadBytes: 0,
		MinFreeBytes:  0,
		KeepSource:    true,
	})
	if err != nil {
		t.Fatalf("RunZipRepack real 7z: %v", err)
	}
	wantDest := convert.ZipRepackDestPath(zipPath)
	if dest != wantDest {
		t.Fatalf("dest=%q want %q", dest, wantDest)
	}
	if meta.Method != convert.MethodZipRepack {
		t.Fatalf("method=%q want %q", meta.Method, convert.MethodZipRepack)
	}
	if meta.OriginalSizeBytes != srcSt.Size() {
		t.Fatalf("original_size_bytes=%d want %d", meta.OriginalSizeBytes, srcSt.Size())
	}
	dstSt, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("dest 7z missing: %v", err)
	}
	minOK := convert.ZipRepackMinOKSize(srcSt.Size())
	if dstSt.Size() < minOK {
		t.Fatalf("dest size %d < minOK %d", dstSt.Size(), minOK)
	}
	if meta.ConvertedSizeBytes != dstSt.Size() {
		t.Fatalf("converted_size_bytes=%d want %d", meta.ConvertedSizeBytes, dstSt.Size())
	}
	if meta.ConvertDurationSeconds == nil {
		t.Fatal("expected convert_duration_seconds on timed repack")
	}
	side := convert.ReadConvertMetadata(dest)
	if side == nil {
		t.Fatal("expected convert metadata sidecar next to dest 7z")
	}
	if side.Method != convert.MethodZipRepack {
		t.Fatalf("sidecar method=%q", side.Method)
	}
	// Source renamed to backup; work dir cleaned.
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Fatalf("source zip should be renamed away: %v", err)
	}
	if _, err := os.Stat(convert.ZipRepackBackupPath(zipPath)); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(convert.ZipRepackWorkDir(zipPath)); !os.IsNotExist(err) {
		t.Fatal("work dir should be removed after success")
	}
	// Dest .7z already beside original path → ShouldRepackZip false if a zip
	// reappears at the same name (idempotent gate).
	restored := filepath.Join(dir, "nested-with-archives.zip")
	if err := writeZipMember(restored, "payloads/again.7z", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldRepackZip(cfg, restored) {
		t.Fatal("ShouldRepackZip false when dest .7z already exists beside zip")
	}
}
