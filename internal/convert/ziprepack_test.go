package convert_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

func zipWithMember(t *testing.T, dest, memberName string, payload []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	wr, err := w.Create(memberName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestMemberLooksLikeEmbeddedArchive(t *testing.T) {
	t.Parallel()
	if !convert.MemberLooksLikeEmbeddedArchive("logs/foo.tgz") {
		t.Fatal("tgz")
	}
	if !convert.MemberLooksLikeEmbeddedArchive("nested/archive.tar.gz") {
		t.Fatal("tar.gz")
	}
	if convert.MemberLooksLikeEmbeddedArchive("readme.txt") {
		t.Fatal("txt")
	}
	if convert.MemberLooksLikeEmbeddedArchive("dir/") {
		t.Fatal("dir")
	}
	if !convert.MemberLooksLikeEmbeddedArchive(`nested\foo.7z`) {
		t.Fatal("backslash 7z")
	}
}

func TestZipHasEmbeddedArchives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := zipWithMember(t, filepath.Join(dir, "plain.zip"), "readme.txt", []byte("x"))
	if convert.ZipHasEmbeddedArchives(plain) {
		t.Fatal("plain")
	}
	nested := zipWithMember(t, filepath.Join(dir, "nested.zip"), "bundle.tgz", []byte("fake-tar"))
	if !convert.ZipHasEmbeddedArchives(nested) {
		t.Fatal("nested")
	}
}

func TestShouldRepackZip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "bundle.tgz", []byte("x"))
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_zip_to_7z":   true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("expected true")
	}
	cfg.ConvertZipTo7z = false
	if convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("zip flag off")
	}
	cfg.ConvertZipTo7z = true
	cfg.Convert7zNonsolid = false
	if convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("nonsolid off")
	}
	// Dest already exists
	cfg.Convert7zNonsolid = true
	dest := convert.ZipRepackDestPath(zipPath)
	if err := os.WriteFile(dest, []byte("7z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("dest exists")
	}
	os.Remove(dest)
	// Metadata on dest path
	if _, err := convert.WriteConvertMetadata(dest, convert.BuildConvertMetadata(1, 1, convert.MethodZipRepack, nil)); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldRepackZip(cfg, zipPath) {
		t.Fatal("metadata on dest")
	}
}

func TestShouldPreconvert_zip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "bundle.tgz", []byte("x"))
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_zip_to_7z":   true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !convert.ShouldPreconvert(cfg, zipPath, convert.ResolveOptions{SearchPathDisabled: true}, nil) {
		t.Fatal("expected preconvert for zip")
	}
}

func TestEstimateZipRepackPeakDiskBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "bundle.tgz", []byte("1234567890"))
	est, err := convert.EstimateZipRepackPeakDiskBytes(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(zipPath)
	if est.SourceBytes != st.Size() {
		t.Fatalf("source %d vs %d", est.SourceBytes, st.Size())
	}
	if est.UncompressedBytes != 10 {
		t.Fatalf("uncomp=%d", est.UncompressedBytes)
	}
	if est.PeakBytes != est.SourceBytes+20 {
		t.Fatalf("peak=%d", est.PeakBytes)
	}
}

func TestBuildZipRepackCmds(t *testing.T) {
	t.Parallel()
	extract := convert.BuildZipExtractCmd("/usr/bin/7z", "/data/a.zip", "/data/a.zip.repack.work")
	if extract[0] != "/usr/bin/7z" || extract[1] != "x" || extract[2] != "-y" {
		t.Fatalf("extract=%v", extract)
	}
	if extract[3] != "-o/data/a.zip.repack.work"+string(filepath.Separator) {
		t.Fatalf("out flag=%q", extract[3])
	}
	if extract[4] != "/data/a.zip" {
		t.Fatalf("src=%q", extract[4])
	}

	create := convert.BuildZipCreate7zCmd("7z", "/data/a.7z.partial")
	want := []string{"7z", "a", "-t7z", "-ms=off", "-mx=0", "-y", "/data/a.7z.partial", "*"}
	if len(create) != len(want) {
		t.Fatalf("create=%v", create)
	}
	for i := range want {
		if create[i] != want[i] {
			t.Fatalf("i=%d got %q want %q", i, create[i], want[i])
		}
	}
}

func TestZipRepackPaths(t *testing.T) {
	t.Parallel()
	zipPath := "/data/sample.zip"
	if got := convert.ZipRepackDestPath(zipPath); got != "/data/sample.7z" {
		t.Fatalf("dest=%q", got)
	}
	if got := convert.ZipRepackPartialPath("/data/sample.7z"); got != "/data/sample.7z.partial" {
		t.Fatalf("partial=%q", got)
	}
	if got := convert.ZipRepackWorkDir(zipPath); got != "/data/sample.zip.repack.work" {
		t.Fatalf("work=%q", got)
	}
	if got := convert.ZipRepackBackupPath(zipPath); got != "/data/sample.zip.pre-repack.bak" {
		t.Fatalf("bak=%q", got)
	}
	if convert.ZipRepackMinOKSize(10000) != 2500 {
		t.Fatal("minok large")
	}
	if convert.ZipRepackMinOKSize(100) != 1024 {
		t.Fatal("minok floor")
	}
	if convert.MethodZipRepack != "zip-repack-7z" {
		t.Fatal(convert.MethodZipRepack)
	}
}

func TestIsZipPath(t *testing.T) {
	t.Parallel()
	if !convert.IsZipPath("a.ZIP") || convert.IsZipPath("a.7z") {
		t.Fatal("IsZipPath")
	}
}
