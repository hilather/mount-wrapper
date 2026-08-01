package archives_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/archives"
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/state"
)

func testCfg(t *testing.T, overrides map[string]any) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		SourceDirs:                   []string{filepath.Join(root, "src")},
		MountRoot:                    filepath.Join(root, "mounts"),
		IndexDir:                     filepath.Join(root, "indexes"),
		OverlayDir:                   filepath.Join(root, "overlays"),
		StateDB:                      filepath.Join(root, "state.db"),
		HooksDir:                     filepath.Join(root, "hooks.d"),
		ArchivesDir:                  filepath.Join(root, "archives"),
		MoveArchivesToLinux:          true,
		ArchiveRelocateOverheadBytes: 1024,
		MinFreeBytes:                 4096,
	}
	for k, v := range overrides {
		switch k {
		case "move_archives_to_linux":
			cfg.MoveArchivesToLinux = v.(bool)
		case "archives_dir":
			cfg.ArchivesDir = v.(string)
		case "archiveconverter_output_dir":
			cfg.ArchiveconverterOutputDir = v.(string)
		case "min_free_bytes":
			cfg.MinFreeBytes = v.(int)
		case "archive_relocate_overhead_bytes":
			cfg.ArchiveRelocateOverheadBytes = v.(int)
		default:
			t.Fatalf("unknown override %q", k)
		}
	}
	_ = os.MkdirAll(cfg.SourceDirs[0], 0o755)
	_ = os.MkdirAll(cfg.ArchivesDir, 0o755)
	return cfg
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func recFor(path, id string) *state.ArchiveRecord {
	if id == "" {
		id = "01234567-89ab-cdef-0123-456789abcdef"
	}
	st, err := os.Stat(path)
	size := int64(0)
	mtime := int64(0)
	if err == nil {
		size = st.Size()
		mtime = st.ModTime().UnixNano()
	}
	return &state.ArchiveRecord{
		ArchiveID:       id,
		SourceDir:       filepath.Dir(path),
		ArchivePath:     path,
		ArchiveBasename: filepath.Base(path),
		SizeBytes:       size,
		MtimeNs:         mtime,
		Fingerprint:     "fp",
		Status:          state.StatusDiscovered,
	}
}

func TestShouldRelocate_RequiresMoveEnabled(t *testing.T) {
	cfg := testCfg(t, map[string]any{"move_archives_to_linux": false})
	src := filepath.Join(cfg.SourceDirs[0], "a.7z")
	writeFile(t, src, []byte(strings.Repeat("x", 100)))
	if archives.ShouldRelocate(cfg, recFor(src, "")) {
		t.Fatal("expected false when move disabled")
	}
}

func TestShouldRelocate_SkipsAlreadyUnderArchivesDir(t *testing.T) {
	cfg := testCfg(t, nil)
	dest := filepath.Join(cfg.ArchivesDir, "a.7z")
	writeFile(t, dest, []byte(strings.Repeat("x", 100)))
	rec := recFor(dest, "")
	rec.SourceDir = cfg.SourceDirs[0]
	if archives.ShouldRelocate(cfg, rec) {
		t.Fatal("expected false when already under archives_dir")
	}
}

func TestShouldRelocate_TrueForSourceOutside(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.SourceDirs[0], "a.7z")
	writeFile(t, src, []byte("data"))
	if !archives.ShouldRelocate(cfg, recFor(src, "")) {
		t.Fatal("expected true for source outside archives_dir")
	}
}

func TestArchiveFilePath_UsesBasenameNotUUID(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.SourceDirs[0], "SUP-12345.7z")
	writeFile(t, src, []byte("x"))
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dest, err := archives.ArchiveFilePath(cfg, recFor(src, id), "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "SUP-12345.7z" {
		t.Fatalf("basename=%s", filepath.Base(dest))
	}
	if strings.Contains(dest, id) {
		t.Fatalf("uuid should not appear in path: %s", dest)
	}
}

func TestArchiveFilePath_DisambiguatesNameCollision(t *testing.T) {
	cfg := testCfg(t, nil)
	existing := filepath.Join(cfg.ArchivesDir, "SUP-1.7z")
	writeFile(t, existing, []byte("other"))
	src := filepath.Join(cfg.SourceDirs[0], "SUP-1.7z")
	writeFile(t, src, []byte("new"))
	id := "abcdef01-2345-6789-abcd-ef0123456789"
	dest, err := archives.ArchiveFilePath(cfg, recFor(src, id), "")
	if err != nil {
		t.Fatal(err)
	}
	want := "SUP-1--" + id[:8] + ".7z"
	if filepath.Base(dest) != want {
		t.Fatalf("got %s want %s", filepath.Base(dest), want)
	}
}

func TestArchiveFilePath_SameFileReturnsPrimary(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.ArchivesDir, "same.7z")
	writeFile(t, src, []byte("x"))
	dest, err := archives.ArchiveFilePath(cfg, recFor(src, "deadbeef-0000-0000-0000-000000000000"), "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "same.7z" {
		t.Fatalf("got %s", filepath.Base(dest))
	}
}

func TestArchiveFilePath_RequiresArchivesDir(t *testing.T) {
	cfg := &config.Config{MoveArchivesToLinux: true}
	_, err := archives.ArchiveFilePath(cfg, recFor("/tmp/x.7z", "id"), "")
	var ae *archives.ArchiveRelocateError
	if !errors.As(err, &ae) {
		t.Fatalf("want ArchiveRelocateError, got %v", err)
	}
	if !strings.Contains(ae.Error(), "archives_dir is not configured") {
		t.Fatalf("msg=%s", ae.Error())
	}
}

func TestRelocateArchive_MovesAndRemovesSource(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.SourceDirs[0], "SUP-1.7z")
	writeFile(t, src, []byte("archive-bytes"))
	rec := recFor(src, "")
	dest, err := archives.RelocateArchive(cfg, rec, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be removed")
	}
	if filepath.Base(dest) != "SUP-1.7z" {
		t.Fatalf("basename=%s", filepath.Base(dest))
	}
	want, err := archives.ArchiveFilePath(cfg, rec, "")
	if err != nil {
		t.Fatal(err)
	}
	// After move, same basename primary; path should match.
	if dest != want && !sameFilePath(t, dest, want) {
		// ArchiveFilePath after move may still compute same primary.
		if filepath.Base(dest) != filepath.Base(want) {
			t.Fatalf("dest=%s want=%s", dest, want)
		}
	}
}

func sameFilePath(t *testing.T, a, b string) bool {
	t.Helper()
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func TestRelocateArchive_MovesSidecar(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.SourceDirs[0], "with-meta.7z")
	writeFile(t, src, []byte("archive"))
	sidecar := convert.MetadataPath(src)
	writeFile(t, sidecar, []byte(`{"method":"flatten"}`))
	dest, err := archives.RelocateArchive(cfg, recFor(src, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(convert.MetadataPath(dest)); err != nil {
		t.Fatalf("sidecar not moved: %v", err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("source sidecar should be gone")
	}
}

func TestRelocateArchive_InsufficientSpace(t *testing.T) {
	cfg := testCfg(t, nil)
	src := filepath.Join(cfg.SourceDirs[0], "big.7z")
	writeFile(t, src, []byte(strings.Repeat("x", 5000)))
	restore := archives.SetFreeBytesFunc(func(string) (int64, bool) {
		return 100, true
	})
	defer restore()

	_, err := archives.RelocateArchive(cfg, recFor(src, ""), "")
	var ae *archives.ArchiveRelocateError
	if !errors.As(err, &ae) {
		t.Fatalf("want ArchiveRelocateError, got %v", err)
	}
	if !strings.Contains(ae.Error(), "insufficient_space_for_relocate") {
		t.Fatalf("msg=%s", ae.Error())
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source should remain after failed relocate")
	}
}

func TestCheckRelocateSpace_UnknownFreeAllows(t *testing.T) {
	cfg := testCfg(t, nil)
	restore := archives.SetFreeBytesFunc(func(string) (int64, bool) {
		return 0, false
	})
	defer restore()
	if err := archives.CheckRelocateSpace(cfg, 1<<30); err != nil {
		t.Fatalf("unknown free should allow: %v", err)
	}
}

func TestCheckRelocateSpace_OkWhenEnough(t *testing.T) {
	cfg := testCfg(t, nil)
	restore := archives.SetFreeBytesFunc(func(string) (int64, bool) {
		// archive 100 + min 4096 + overhead 1024 = 5220
		return 10000, true
	})
	defer restore()
	if err := archives.CheckRelocateSpace(cfg, 100); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveSupersededSource_RemovesDrvFsOriginal(t *testing.T) {
	cfg := testCfg(t, map[string]any{
		"archiveconverter_output_dir": filepath.Join(t.TempDir(), "converted"),
	})
	original := filepath.Join(cfg.SourceDirs[0], "SUP-1.7z")
	writeFile(t, original, []byte("original"))
	active := filepath.Join(cfg.ArchivesDir, "SUP-1.7z")
	writeFile(t, active, []byte("converted"))
	rec := recFor(original, "")

	ok := archives.RemoveSupersededSource(cfg, original, active, rec.ArchiveID)
	if !ok {
		t.Fatal("expected remove true")
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("original should be gone")
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatal("active should remain")
	}
}

func TestRemoveSupersededSource_RemovesSidecar(t *testing.T) {
	cfg := testCfg(t, nil)
	original := filepath.Join(cfg.SourceDirs[0], "a.7z")
	writeFile(t, original, []byte("x"))
	writeFile(t, convert.MetadataPath(original), []byte("{}"))
	active := filepath.Join(cfg.ArchivesDir, "a.7z")
	writeFile(t, active, []byte("y"))

	if !archives.RemoveSupersededSource(cfg, original, active, "id") {
		t.Fatal("expected true")
	}
	if _, err := os.Stat(convert.MetadataPath(original)); !os.IsNotExist(err) {
		t.Fatal("sidecar should be gone")
	}
}

func TestRemoveSupersededSource_SkipsWhenMoveDisabled(t *testing.T) {
	cfg := testCfg(t, map[string]any{"move_archives_to_linux": false})
	original := filepath.Join(cfg.SourceDirs[0], "a.7z")
	writeFile(t, original, []byte("x"))
	active := filepath.Join(cfg.ArchivesDir, "a.7z")
	writeFile(t, active, []byte("y"))
	if archives.RemoveSupersededSource(cfg, original, active, "id") {
		t.Fatal("expected false")
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("original should remain")
	}
}

func TestRemoveSupersededSource_SkipsArchivesPath(t *testing.T) {
	cfg := testCfg(t, nil)
	original := filepath.Join(cfg.ArchivesDir, "keep.7z")
	writeFile(t, original, []byte("x"))
	active := filepath.Join(cfg.ArchivesDir, "other.7z")
	writeFile(t, active, []byte("y"))
	if archives.RemoveSupersededSource(cfg, original, active, "id") {
		t.Fatal("must not remove files under archives_dir")
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("should remain")
	}
}

func TestRemoveSupersededSource_SkipsSamePath(t *testing.T) {
	cfg := testCfg(t, nil)
	path := filepath.Join(cfg.SourceDirs[0], "a.7z")
	writeFile(t, path, []byte("x"))
	if archives.RemoveSupersededSource(cfg, path, path, "id") {
		t.Fatal("same path should not remove")
	}
}

func TestIsConvertedOutputPath_RelocateParity(t *testing.T) {
	converted := filepath.Join(t.TempDir(), "converted")
	cfg := testCfg(t, map[string]any{"archiveconverter_output_dir": converted})
	out := filepath.Join(converted, "out.7z")
	writeFile(t, out, []byte("x"))
	if !archives.IsConvertedOutputPath(cfg, out) {
		t.Fatal("expected true")
	}
	if archives.IsConvertedOutputPath(cfg, filepath.Join(cfg.SourceDirs[0], "a.7z")) {
		t.Fatal("expected false")
	}
}

func TestArchiveFilePath_EmptyBasenameFallback(t *testing.T) {
	cfg := testCfg(t, nil)
	rec := &state.ArchiveRecord{
		ArchiveID:       "12345678-aaaa-bbbb-cccc-dddddddddddd",
		ArchivePath:     filepath.Join(cfg.SourceDirs[0], "x"),
		ArchiveBasename: ".",
	}
	dest, err := archives.ArchiveFilePath(cfg, rec, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "archive-12345678.bin" {
		t.Fatalf("got %s", filepath.Base(dest))
	}
}
