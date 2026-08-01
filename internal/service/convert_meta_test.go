package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/service"
)

func TestConvertSidecarMeta_FromSidecar(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "converted.7z")
	if err := os.WriteFile(archive, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dur := 4.25
	meta := convert.BuildConvertMetadata(12_000, 9_000, convert.MethodZipRepack, &dur)
	if _, err := convert.WriteConvertMetadata(archive, meta); err != nil {
		t.Fatal(err)
	}

	got := service.ConvertSidecarMeta{}.ReadConvertMetadata(archive)
	if got == nil {
		t.Fatal("expected convert metadata from sidecar")
	}
	if got.OriginalSizeBytes != 12_000 {
		t.Fatalf("original=%d want 12000", got.OriginalSizeBytes)
	}
	if got.SizeDeltaBytes != -3_000 {
		t.Fatalf("delta=%d want -3000", got.SizeDeltaBytes)
	}
	if got.ConvertDurationSeconds == nil || *got.ConvertDurationSeconds != dur {
		t.Fatalf("duration=%v want %v", got.ConvertDurationSeconds, dur)
	}
}

func TestConvertSidecarMeta_MissingReturnsNil(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "plain.tar")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var meta service.ConvertSidecarMeta
	if got := meta.ReadConvertMetadata(archive); got != nil {
		t.Fatalf("expected nil without sidecar, got %#v", got)
	}
	if got := meta.ReadConvertMetadata(""); got != nil {
		t.Fatalf("expected nil for empty path, got %#v", got)
	}
}

func TestNoConvertMeta_StillEmpty(t *testing.T) {
	t.Parallel()
	if got := (metrics.NoConvertMeta{}).ReadConvertMetadata("/any/path"); got != nil {
		t.Fatalf("NoConvertMeta must stay empty, got %#v", got)
	}
}

func TestConvertSidecarMeta_FromOuterCachePath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "solid.7z")
	if err := os.WriteFile(src, []byte("solid-payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := convert.NonsolidCacheDestPath(cache, src)
	if err := os.WriteFile(dest, []byte("nonsolid"), 0o644); err != nil {
		t.Fatal(err)
	}
	dur := 1.25
	if _, err := convert.WriteConvertMetadata(dest, convert.BuildConvertMetadata(13_000, 11_000, convert.MethodOuterNonsolidCLI, &dur)); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Convert7zNonsolid:  true,
		Convert7zScope:     "outer",
		Convert7zCacheDir:  cache,
	}
	got := service.ConvertSidecarMeta{Config: cfg}.ReadConvertMetadata(src)
	if got == nil {
		t.Fatal("expected convert metadata from outer cache sidecar")
	}
	if got.OriginalSizeBytes != 13_000 {
		t.Fatalf("original=%d want 13000", got.OriginalSizeBytes)
	}
	if got.SizeDeltaBytes != -2_000 {
		t.Fatalf("delta=%d want -2000", got.SizeDeltaBytes)
	}
	if got.ConvertDurationSeconds == nil || *got.ConvertDurationSeconds != dur {
		t.Fatalf("duration=%v want %v", got.ConvertDurationSeconds, dur)
	}

	// Without Config, cache sidecar is not discovered.
	if got := (service.ConvertSidecarMeta{}).ReadConvertMetadata(src); got != nil {
		t.Fatalf("without Config expected nil, got %#v", got)
	}
}

func TestConvertSidecarMeta_ComputeUsesSidecarWhenStoreEmpty(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "out.7z")
	if err := os.WriteFile(archive, make([]byte, 8000), 0o644); err != nil {
		t.Fatal(err)
	}
	dur := 9.5
	if _, err := convert.WriteConvertMetadata(archive, convert.BuildConvertMetadata(10_000, 8_000, "flatten", &dur)); err != nil {
		t.Fatal(err)
	}

	in := metrics.ArchiveInput{
		ArchiveID:   "c1",
		ArchivePath: archive,
	}
	m := metrics.ComputeArchiveMetrics(
		in,
		metrics.FSSizeProvider{},
		metrics.MapExtractedProvider{},
		service.ConvertSidecarMeta{},
		metrics.ComputeOptions{},
	)
	if m.ConvertSourceSizeBytes == nil || *m.ConvertSourceSizeBytes != 10_000 {
		t.Fatalf("convert_source=%v want 10000", m.ConvertSourceSizeBytes)
	}
	if m.ConvertSizeDeltaBytes == nil || *m.ConvertSizeDeltaBytes != -2_000 {
		t.Fatalf("convert_delta=%v want -2000", m.ConvertSizeDeltaBytes)
	}
	if m.ConvertDurationSeconds == nil || *m.ConvertDurationSeconds != dur {
		t.Fatalf("duration=%v want %v", m.ConvertDurationSeconds, dur)
	}
}

func TestConvertSidecarMeta_StoreFieldsPreferredOverSidecar(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "out.7z")
	if err := os.WriteFile(archive, make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sidecar would claim different sizes; store fields should win when both set.
	sidecarDur := 99.0
	if _, err := convert.WriteConvertMetadata(archive, convert.BuildConvertMetadata(99_000, 50_000, "flatten", &sidecarDur)); err != nil {
		t.Fatal(err)
	}

	storeSrc := int64(12_000)
	storeDur := 3.5
	in := metrics.ArchiveInput{
		ArchiveID:              "c2",
		ArchivePath:            archive,
		ConvertSourceSizeBytes: &storeSrc,
		ConvertDurationSeconds: &storeDur,
	}
	m := metrics.ComputeArchiveMetrics(
		in,
		metrics.FSSizeProvider{},
		metrics.MapExtractedProvider{},
		service.ConvertSidecarMeta{},
		metrics.ComputeOptions{},
	)
	if m.ConvertSourceSizeBytes == nil || *m.ConvertSourceSizeBytes != storeSrc {
		t.Fatalf("convert_source=%v want store %d", m.ConvertSourceSizeBytes, storeSrc)
	}
	if m.ConvertDurationSeconds == nil || *m.ConvertDurationSeconds != storeDur {
		t.Fatalf("duration=%v want store %v", m.ConvertDurationSeconds, storeDur)
	}
	// Both store fields set → delta is archive_size − convert_source (not sidecar delta).
	if m.ConvertSizeDeltaBytes == nil || *m.ConvertSizeDeltaBytes != 5000-storeSrc {
		t.Fatalf("convert_delta=%v want %d", m.ConvertSizeDeltaBytes, 5000-storeSrc)
	}
}
