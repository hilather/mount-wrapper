package archives_test

import (
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/archives"
	"github.com/hilather/mount-wrapper/internal/config"
)

func TestIsArchivesPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ArchivesDir: dir}
	inside := filepath.Join(dir, "a.7z")
	if !archives.IsArchivesPath(cfg, inside) {
		t.Fatalf("expected inside path true")
	}
	if !archives.IsArchivesPath(cfg, dir) {
		t.Fatalf("expected root true")
	}
	outside := filepath.Join(filepath.Dir(dir), "other", "x.7z")
	if archives.IsArchivesPath(cfg, outside) {
		t.Fatalf("expected outside false")
	}
	if archives.IsArchivesPath(&config.Config{}, inside) {
		t.Fatalf("empty archives_dir should be false")
	}
}

func TestIsConvertedOutputPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ArchiveconverterOutputDir: dir}
	if !archives.IsConvertedOutputPath(cfg, filepath.Join(dir, "out.7z")) {
		t.Fatal("expected true")
	}
	if archives.IsConvertedOutputPath(cfg, filepath.Join(t.TempDir(), "x")) {
		t.Fatal("expected false")
	}
}

func TestArchivesDirPath(t *testing.T) {
	if archives.ArchivesDirPath(nil) != "" {
		t.Fatal("nil cfg")
	}
	if archives.ArchivesDirPath(&config.Config{ArchivesDir: "  "}) != "" {
		t.Fatal("blank")
	}
	if got := archives.ArchivesDirPath(&config.Config{ArchivesDir: "/var/lib/mw/archives"}); got != "/var/lib/mw/archives" {
		t.Fatalf("got %q", got)
	}
}
