package convert_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestMetadataPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/var/lib/archives/a.7z", "/var/lib/archives/a.7z.tarmount-convert.json"},
		{"SUP-1.7z", "SUP-1.7z.tarmount-convert.json"},
	}
	for _, tc := range cases {
		if got := convert.MetadataPath(tc.in); got != tc.want {
			t.Errorf("MetadataPath(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	if convert.MetadataSuffix != ".tarmount-convert.json" {
		t.Fatalf("MetadataSuffix=%q", convert.MetadataSuffix)
	}
}

func TestWriteAndReadMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "sample.7z")
	if err := os.WriteFile(archive, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	dur := 42.5
	meta := convert.ConvertMetadata{
		OriginalSizeBytes:      1000,
		ConvertedSizeBytes:     1500,
		ConvertedAt:            "2026-07-26T21:00:00Z",
		Method:                 "flatten",
		ConvertDurationSeconds: &dur,
	}
	sidecar, err := convert.WriteConvertMetadata(archive, meta)
	if err != nil {
		t.Fatal(err)
	}
	if sidecar != convert.MetadataPath(archive) {
		t.Fatalf("sidecar=%q", sidecar)
	}
	loaded := convert.ReadConvertMetadata(archive)
	if loaded == nil {
		t.Fatal("expected metadata")
	}
	if loaded.OriginalSizeBytes != 1000 || loaded.ConvertedSizeBytes != 1500 {
		t.Fatalf("sizes orig=%d conv=%d", loaded.OriginalSizeBytes, loaded.ConvertedSizeBytes)
	}
	if loaded.SizeDelta() != 500 || loaded.SizeDeltaBytes != 500 {
		t.Fatalf("delta=%d stored=%d", loaded.SizeDelta(), loaded.SizeDeltaBytes)
	}
	if loaded.ConvertDurationSeconds == nil || *loaded.ConvertDurationSeconds != 42.5 {
		t.Fatalf("duration=%v", loaded.ConvertDurationSeconds)
	}
	if loaded.Method != "flatten" {
		t.Fatalf("method=%q", loaded.Method)
	}
	// On-disk JSON includes size_delta_bytes.
	raw, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["size_delta_bytes"].(float64) != 500 {
		t.Fatalf("json size_delta=%v", m["size_delta_bytes"])
	}
}

func TestReadMetadataWithoutDuration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "legacy.7z")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := convert.MetadataPath(archive)
	body := `{"converted_at":"2026-07-26T21:00:00Z","converted_size_bytes":2,` +
		`"original_size_bytes":1,"size_delta_bytes":1,"method":"flatten"}` + "\n"
	if err := os.WriteFile(sidecar, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := convert.ReadConvertMetadata(archive)
	if loaded == nil {
		t.Fatal("expected metadata")
	}
	if loaded.ConvertDurationSeconds != nil {
		t.Fatalf("duration should be nil, got %v", *loaded.ConvertDurationSeconds)
	}
	if loaded.OriginalSizeBytes != 1 || loaded.ConvertedSizeBytes != 2 {
		t.Fatalf("sizes %d %d", loaded.OriginalSizeBytes, loaded.ConvertedSizeBytes)
	}
}

func TestMissingMetadataReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "missing.7z")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if convert.ReadConvertMetadata(archive) != nil {
		t.Fatal("expected nil")
	}
	if convert.HasConvertMetadata(archive) {
		t.Fatal("expected false")
	}
}

func TestBuildConvertMetadataDefaults(t *testing.T) {
	t.Parallel()
	m := convert.BuildConvertMetadata(10, 20, "", nil)
	if m.Method != "flatten" {
		t.Fatalf("method=%q", m.Method)
	}
	if m.SizeDeltaBytes != 10 {
		t.Fatalf("delta=%d", m.SizeDeltaBytes)
	}
	if m.ConvertedAt == "" {
		t.Fatal("converted_at empty")
	}
}
