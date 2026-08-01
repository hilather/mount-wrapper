package api

import (
	"testing"
)

func TestDiffSnapshots_noChange(t *testing.T) {
	snap := map[string]any{
		"ok":       true,
		"counts":   map[string]any{"mounted": 1, "discovered": 0},
		"mounted":  1,
		"low_disk": false,
		"archives": []any{
			map[string]any{"archive_id": "a1", "status": "mounted", "progress_label": ""},
		},
		"last_scan_at": "2026-01-01T00:00:00Z",
	}
	d := DiffSnapshots(snap, snap)
	if d.HasChanges() {
		t.Fatalf("expected no changes, got %+v", d)
	}
}

func TestDiffSnapshots_countsAndArchive(t *testing.T) {
	prev := map[string]any{
		"ok":      true,
		"counts":  map[string]any{"mounted": 0, "indexing": 1},
		"mounted": 0, "indexing": 1,
		"low_disk": false,
		"archives": []any{
			map[string]any{
				"archive_id":     "a1",
				"status":         "indexing",
				"progress_label": "building index",
				"elapsed_s":      1.0,
				"last_seen_at":   "t0", // watched? no — should not alone trigger
			},
		},
		"last_scan_at": "2026-01-01T00:00:00Z",
	}
	curr := map[string]any{
		"ok":      true,
		"counts":  map[string]any{"mounted": 1, "indexing": 0},
		"mounted": 1, "indexing": 0,
		"low_disk": false,
		"archives": []any{
			map[string]any{
				"archive_id":     "a1",
				"status":         "mounted",
				"progress_label": "",
				"elapsed_s":      nil,
				"last_seen_at":   "t1", // alone would not matter; status already changed
			},
		},
		"last_scan_at": "2026-01-01T00:00:00Z",
	}
	d := DiffSnapshots(prev, curr)
	if d.Counts == nil {
		t.Fatal("expected counts event")
	}
	if d.Counts["mounted"] != 1 {
		t.Fatalf("counts mounted=%v", d.Counts["mounted"])
	}
	if len(d.Archives) != 1 {
		t.Fatalf("archives len=%d", len(d.Archives))
	}
	if d.Archives[0]["status"] != "mounted" {
		t.Fatalf("archive status=%v", d.Archives[0]["status"])
	}
	if d.LowDisk != nil || d.Scan != nil {
		t.Fatalf("unexpected low_disk/scan: %+v", d)
	}
}

func TestDiffSnapshots_lastSeenOnly_noArchiveEvent(t *testing.T) {
	prev := map[string]any{
		"ok":     true,
		"counts": map[string]any{"mounted": 1},
		"mounted": 1,
		"archives": []any{
			map[string]any{"archive_id": "a1", "status": "mounted", "last_seen_at": "t0"},
		},
	}
	curr := map[string]any{
		"ok":     true,
		"counts": map[string]any{"mounted": 1},
		"mounted": 1,
		"archives": []any{
			map[string]any{"archive_id": "a1", "status": "mounted", "last_seen_at": "t1"},
		},
	}
	d := DiffSnapshots(prev, curr)
	if len(d.Archives) != 0 {
		t.Fatalf("last_seen_at alone should not emit archive: %+v", d.Archives)
	}
	if d.HasChanges() {
		t.Fatalf("expected no changes: %+v", d)
	}
}

func TestDiffSnapshots_lowDiskEdgeAndScan(t *testing.T) {
	prev := map[string]any{
		"ok":           true,
		"counts":       map[string]any{"mounted": 0},
		"low_disk":     false,
		"last_scan_at": "2026-01-01T00:00:00Z",
		"archives":     []any{},
	}
	curr := map[string]any{
		"ok":                    true,
		"counts":                map[string]any{"mounted": 0},
		"low_disk":              true,
		"disk_free_bytes":       int64(100),
		"min_free_bytes":        1000,
		"last_scan_at":          "2026-01-01T00:01:00Z",
		"last_scan":             map[string]any{"seen": 3},
		"last_scan_duration_ms": 12.5,
		"archives":              []any{},
	}
	d := DiffSnapshots(prev, curr)
	if d.LowDisk == nil || d.LowDisk["low_disk"] != true {
		t.Fatalf("low_disk=%v", d.LowDisk)
	}
	if d.LowDisk["disk_free_bytes"] != int64(100) {
		t.Fatalf("disk_free=%v", d.LowDisk["disk_free_bytes"])
	}
	if d.Scan == nil || d.Scan["last_scan_at"] != "2026-01-01T00:01:00Z" {
		t.Fatalf("scan=%v", d.Scan)
	}
	if d.Scan["last_scan"] == nil {
		t.Fatal("expected last_scan in scan event")
	}
	// counts also re-emitted because low_disk / last_scan_at ride on counts payload
	if d.Counts == nil {
		t.Fatal("expected counts when low_disk/scan change")
	}
}

func TestDiffSnapshots_removedAndNewArchive(t *testing.T) {
	prev := map[string]any{
		"ok": true,
		"archives": []any{
			map[string]any{"archive_id": "gone", "status": "mounted"},
			map[string]any{"archive_id": "keep", "status": "mounted"},
		},
		"counts": map[string]any{"mounted": 2},
		"mounted": 2,
	}
	curr := map[string]any{
		"ok": true,
		"archives": []any{
			map[string]any{"archive_id": "keep", "status": "mounted"},
			map[string]any{"archive_id": "new", "status": "discovered"},
		},
		"counts": map[string]any{"mounted": 1, "discovered": 1},
		"mounted": 1, "discovered": 1,
	}
	d := DiffSnapshots(prev, curr)
	if len(d.RemovedIDs) != 1 || d.RemovedIDs[0] != "gone" {
		t.Fatalf("removed=%v", d.RemovedIDs)
	}
	// new should appear; keep unchanged
	if len(d.Archives) != 1 || d.Archives[0]["archive_id"] != "new" {
		t.Fatalf("archives=%v", d.Archives)
	}
	payload := d.ArchiveEventPayload()
	if payload == nil {
		t.Fatal("expected archive payload")
	}
	ids, _ := payload["removed_ids"].([]string)
	if len(ids) != 1 || ids[0] != "gone" {
		t.Fatalf("payload removed_ids=%v", payload["removed_ids"])
	}
}

func TestDiffSnapshots_metricsSummary(t *testing.T) {
	prev := map[string]any{
		"ok":              true,
		"metrics_summary": map[string]any{"archive_count": 1},
		"archives":        []any{},
		"counts":          map[string]any{},
	}
	curr := map[string]any{
		"ok":              true,
		"metrics_summary": map[string]any{"archive_count": 2},
		"archives":        []any{},
		"counts":          map[string]any{},
	}
	d := DiffSnapshots(prev, curr)
	if d.Metrics == nil {
		t.Fatal("expected metrics event")
	}
	sum, _ := d.Metrics["metrics_summary"].(map[string]any)
	if sum["archive_count"] != 2 {
		t.Fatalf("summary=%v", sum)
	}
}

func TestDiffSnapshots_nilPrev_allNew(t *testing.T) {
	curr := map[string]any{
		"ok":       true,
		"counts":   map[string]any{"mounted": 1},
		"mounted":  1,
		"low_disk": true,
		"archives": []any{
			map[string]any{"archive_id": "a1", "status": "mounted"},
		},
		"last_scan_at": "t1",
	}
	d := DiffSnapshots(nil, curr)
	if d.Counts == nil || len(d.Archives) != 1 {
		t.Fatalf("expected counts+archive from nil prev: %+v", d)
	}
	// low_disk edge from false(default) → true
	if d.LowDisk == nil {
		t.Fatal("expected low_disk edge from nil prev")
	}
	if d.Scan == nil {
		t.Fatal("expected scan from nil prev when last_scan_at set")
	}
}

func TestDiffSnapshots_errorCurr(t *testing.T) {
	d := DiffSnapshots(map[string]any{"ok": true}, map[string]any{"ok": false, "error": "x"})
	if d.HasChanges() {
		t.Fatalf("error curr should yield empty delta: %+v", d)
	}
}

func TestArchiveEventPayload_empty(t *testing.T) {
	var d SnapshotDelta
	if d.ArchiveEventPayload() != nil {
		t.Fatal("empty delta should not build archive payload")
	}
}
