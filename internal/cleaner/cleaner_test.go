package cleaner_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/cleaner"
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func cfg(t *testing.T, tmp string, overrides map[string]any) *config.Config {
	t.Helper()
	raw := map[string]any{
		"source_dirs":           []any{filepath.Join(tmp, "src")},
		"mount_root":            filepath.Join(tmp, "mounts"),
		"index_dir":             filepath.Join(tmp, "indexes"),
		"overlay_dir":           filepath.Join(tmp, "overlays"),
		"state_db":              filepath.Join(tmp, "state.db"),
		"hooks_dir":             filepath.Join(tmp, "hooks.d"),
		"cleanup_after":         "24h",
		"overlay_cleanup":       "quarantine",
		"quarantine_retain_for": "168h",
		"quarantine_max_bytes":  0,
		"min_free_bytes":        1,
	}
	for k, v := range overrides {
		raw[k] = v
	}
	c, err := config.FromMap(raw, filepath.Join(tmp, "config.yaml"))
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	return c
}

func insertAbsent(t *testing.T, store *state.Store, path string, removedAt string, opts map[string]any) *state.ArchiveRecord {
	t.Helper()
	p := state.InsertDiscoveredParams{
		SourceDir:       filepath.Dir(path),
		ArchivePath:     path,
		ArchiveBasename: filepath.Base(path),
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	}
	if opts != nil {
		if v, ok := opts["index_path"].(string); ok {
			p.IndexPath = &v
		}
		if v, ok := opts["overlay_path"].(string); ok {
			p.OverlayPath = &v
		}
		if v, ok := opts["mount_path"].(string); ok {
			p.MountPath = &v
		}
	}
	rec, err := store.InsertDiscovered(p)
	if err != nil {
		t.Fatalf("InsertDiscovered: %v", err)
	}
	absent, err := store.MarkAbsent(rec.ArchiveID, removedAt, nil)
	if err != nil {
		t.Fatalf("MarkAbsent: %v", err)
	}
	return absent
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func TestGraceCutoffISO(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	cutoff := cleaner.GraceCutoffISO(24*3600, now)
	if cutoff != "2026-01-09T12:00:00Z" {
		t.Fatalf("cutoff=%s", cutoff)
	}
}

func TestPathUnderRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "overlays")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "aid")
	outside := filepath.Join(tmp, "elsewhere")
	if !cleaner.PathUnderRoot(child, root) {
		t.Fatal("child should be under root")
	}
	if !cleaner.PathUnderRoot(root, root) {
		t.Fatal("root equals root")
	}
	if cleaner.PathUnderRoot(outside, root) {
		t.Fatal("outside must not be under root")
	}
	if cleaner.PathUnderRoot("", root) || cleaner.PathUnderRoot(child, "") {
		t.Fatal("empty should be false")
	}
}

// ---------------------------------------------------------------------------
// Overlay policy
// ---------------------------------------------------------------------------

func TestHandleOverlayQuarantine(t *testing.T) {
	tmp := t.TempDir()
	overlayDir := filepath.Join(tmp, "overlays")
	overlay := filepath.Join(overlayDir, "aid1")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	action, dest, err := cleaner.HandleOverlay(overlay, "aid1", overlayDir, "quarantine", now)
	if err != nil {
		t.Fatal(err)
	}
	if action != cleaner.OverlayQuarantined {
		t.Fatalf("action=%s", action)
	}
	if dest == "" || !dirExists(dest) {
		t.Fatalf("dest=%s", dest)
	}
	if dirExists(overlay) {
		t.Fatal("source should be gone")
	}
	body, err := os.ReadFile(filepath.Join(dest, "file.txt"))
	if err != nil || string(body) != "x" {
		t.Fatalf("content=%q err=%v", body, err)
	}
}

func TestHandleOverlayDelete(t *testing.T) {
	tmp := t.TempDir()
	overlayDir := filepath.Join(tmp, "overlays")
	overlay := filepath.Join(overlayDir, "aid1")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	action, dest, err := cleaner.HandleOverlay(overlay, "aid1", overlayDir, "delete", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if action != cleaner.OverlayDeleted || dest != "" {
		t.Fatalf("action=%s dest=%s", action, dest)
	}
	if dirExists(overlay) {
		t.Fatal("should be deleted")
	}
}

func TestHandleOverlayRetain(t *testing.T) {
	tmp := t.TempDir()
	overlayDir := filepath.Join(tmp, "overlays")
	overlay := filepath.Join(overlayDir, "aid1")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	action, dest, err := cleaner.HandleOverlay(overlay, "aid1", overlayDir, "retain", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if action != cleaner.OverlayRetained {
		t.Fatalf("action=%s", action)
	}
	if dest != overlay {
		t.Fatalf("dest=%s", dest)
	}
	if !dirExists(overlay) {
		t.Fatal("should remain")
	}
}

func TestHandleOverlayMissing(t *testing.T) {
	tmp := t.TempDir()
	overlayDir := filepath.Join(tmp, "overlays")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	action, _, err := cleaner.HandleOverlay(
		filepath.Join(overlayDir, "nope"), "aid", overlayDir, "delete", time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if action != cleaner.OverlayMissing {
		t.Fatalf("action=%s", action)
	}
}

func TestHandleOverlayRefusesOutsideRoot(t *testing.T) {
	tmp := t.TempDir()
	overlayDir := filepath.Join(tmp, "overlays")
	outside := filepath.Join(tmp, "evil")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	action, _, err := cleaner.HandleOverlay(outside, "aid", overlayDir, "delete", time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
	if action != cleaner.OverlayRefused {
		t.Fatalf("action=%s", action)
	}
	if !dirExists(outside) {
		t.Fatal("must not delete outside root")
	}
}

// ---------------------------------------------------------------------------
// Purge
// ---------------------------------------------------------------------------

func TestPurgeRemovesRowIndexAndQuarantinesOverlay(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	store := openStore(t)

	idx := filepath.Join(tmp, "indexes", "a.index.sqlite")
	if err := os.MkdirAll(filepath.Dir(idx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idx, []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(tmp, "overlays", "aid")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "w"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(tmp, "mounts", "a.tar.gz")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}

	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "a.tar.gz"),
		ArchiveBasename: "a.tar.gz",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
		IndexPath:       &idx,
		OverlayPath:     &overlay,
		MountPath:       &mount,
	})
	if err != nil {
		t.Fatal(err)
	}

	cl := cleaner.New(c, store)
	cl.Clock = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	result := cl.PurgeArchive(rec.ArchiveID, true)
	if !result.OK {
		t.Fatalf("purge: %+v", result)
	}
	if !result.IndexDeleted {
		t.Fatal("index should be deleted")
	}
	if result.OverlayAction != cleaner.OverlayQuarantined {
		t.Fatalf("overlay=%s", result.OverlayAction)
	}
	if result.OverlayDest == "" || !dirExists(result.OverlayDest) {
		t.Fatalf("dest=%s", result.OverlayDest)
	}
	got, err := store.GetArchive(rec.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("row should be gone")
	}
	if fileExists(idx) {
		t.Fatal("index file should be gone")
	}
	if dirExists(overlay) {
		t.Fatal("overlay source should be gone")
	}

	// Path free for rediscovery with a new id.
	again, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "a.tar.gz"),
		ArchiveBasename: "a.tar.gz",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ArchiveID == rec.ArchiveID {
		t.Fatal("new discovery should get a new id")
	}
}

func TestPurgeDeletePolicy(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"overlay_cleanup": "delete"})
	store := openStore(t)
	overlay := filepath.Join(tmp, "overlays", "x")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "f"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "b.zip"),
		ArchiveBasename: "b.zip",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
		OverlayPath:     &overlay,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, store).PurgeArchive(rec.ArchiveID, true)
	if !result.OK || result.OverlayAction != cleaner.OverlayDeleted {
		t.Fatalf("%+v", result)
	}
	if dirExists(overlay) {
		t.Fatal("overlay should be gone")
	}
}

func TestPurgeRetainPolicy(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"overlay_cleanup": "retain"})
	store := openStore(t)
	overlay := filepath.Join(tmp, "overlays", "keep")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "c.tar"),
		ArchiveBasename: "c.tar",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
		OverlayPath:     &overlay,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, store).PurgeArchive(rec.ArchiveID, true)
	if !result.OK || result.OverlayAction != cleaner.OverlayRetained {
		t.Fatalf("%+v", result)
	}
	if !dirExists(overlay) {
		t.Fatal("overlay should remain")
	}
}

func TestAdminPurgeNotFound(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	store := openStore(t)
	result := cleaner.New(c, store).PurgeArchive("no-such-id", true)
	if result.OK || result.Error != "archive not found" {
		t.Fatalf("%+v", result)
	}
}

func TestPurgeRefusesIndexOutsideIndexDir(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	store := openStore(t)
	outsideIdx := filepath.Join(tmp, "not-indexes", "evil.index")
	if err := os.MkdirAll(filepath.Dir(outsideIdx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideIdx, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "d.tar"),
		ArchiveBasename: "d.tar",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
		IndexPath:       &outsideIdx,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, store).PurgeArchive(rec.ArchiveID, true)
	if !result.OK {
		t.Fatalf("%+v", result)
	}
	if result.IndexDeleted {
		t.Fatal("must not delete index outside index_dir")
	}
	if !fileExists(outsideIdx) {
		t.Fatal("index must remain on disk")
	}
}

// ---------------------------------------------------------------------------
// Grace purge
// ---------------------------------------------------------------------------

func TestGracePurgeOnlyPastGrace(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"cleanup_after": "24h"})
	store := openStore(t)
	old := insertAbsent(t, store, filepath.Join(tmp, "src", "old.tar"), "2020-01-01T00:00:00Z", nil)
	newRec := insertAbsent(t, store, filepath.Join(tmp, "src", "new.tar"), "2099-01-01T00:00:00Z", nil)

	results := cleaner.New(c, store).PurgeAbsentPastGrace()
	ids := map[string]bool{}
	for _, r := range results {
		if r.OK {
			ids[r.ArchiveID] = true
		}
	}
	if !ids[old.ArchiveID] {
		t.Fatal("old should be purged")
	}
	if ids[newRec.ArchiveID] {
		t.Fatal("new should not be purged")
	}
	if got, _ := store.GetArchive(old.ArchiveID); got != nil {
		t.Fatal("old row gone")
	}
	if got, _ := store.GetArchive(newRec.ArchiveID); got == nil {
		t.Fatal("new row kept")
	}
}

func TestReappearClearsRemovedAtCleanerDoesNotPurge(t *testing.T) {
	// Document interaction: scanner Reappear clears removed_at; cleaner grace
	// purge only targets still-absent rows past grace — they do not fight.
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"cleanup_after": "1s"})
	store := openStore(t)
	rec := insertAbsent(t, store, filepath.Join(tmp, "src", "gone.tar"), "2020-01-01T00:00:00Z", nil)

	// Reappear before cleaner runs (scanner would do this).
	back, err := store.Reappear(rec.ArchiveID, 2, 2, "2:2", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != state.StatusDiscovered {
		t.Fatalf("status=%s", back.Status)
	}
	if back.RemovedAt != nil {
		t.Fatalf("removed_at should be cleared: %v", back.RemovedAt)
	}

	results := cleaner.New(c, store).PurgeAbsentPastGrace()
	if len(results) != 0 {
		t.Fatalf("cleaner must not purge reappeared archive: %+v", results)
	}
	got, err := store.GetArchive(rec.ArchiveID)
	if err != nil || got == nil {
		t.Fatalf("row should remain: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Quarantine prune
// ---------------------------------------------------------------------------

func TestQuarantineAgePrune(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"quarantine_retain_for": "1h"})
	q := cleaner.QuarantineDir(filepath.Join(tmp, "overlays"))
	if err := os.MkdirAll(q, 0o755); err != nil {
		t.Fatal(err)
	}
	oldEntry := filepath.Join(q, "old-aid-20200101T000000Z")
	if err := os.MkdirAll(oldEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldEntry, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := time.Now().UTC().Add(-48 * time.Hour)
	if err := os.Chtimes(oldEntry, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(q, "fresh-aid")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "f"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, freed := cleaner.New(c, openStore(t)).PruneQuarantine()
	if removed < 1 || freed < 1 {
		t.Fatalf("removed=%d freed=%d", removed, freed)
	}
	if dirExists(oldEntry) {
		t.Fatal("old entry should be pruned")
	}
	if !dirExists(fresh) {
		t.Fatal("fresh entry should remain")
	}
}

func TestQuarantineSizeCap(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{
		"quarantine_retain_for": "720h",
		"quarantine_max_bytes":  10,
	})
	q := cleaner.QuarantineDir(filepath.Join(tmp, "overlays"))
	if err := os.MkdirAll(q, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"a", "b", "c"} {
		d := filepath.Join(q, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "blob"), make([]byte, 20), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().UTC().Add(-time.Duration(3-i) * time.Hour)
		if err := os.Chtimes(d, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	removed, _ := cleaner.New(c, openStore(t)).PruneQuarantine()
	if removed < 1 {
		t.Fatalf("expected size-cap prune, removed=%d", removed)
	}
	entries, err := os.ReadDir(q)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += cleaner.DirSizeBytes(filepath.Join(q, e.Name()))
	}
	if total > 10 && len(entries) >= 3 {
		t.Fatalf("total=%d remaining=%d", total, len(entries))
	}
}

// ---------------------------------------------------------------------------
// Run summary + mount dirs + temps
// ---------------------------------------------------------------------------

func TestCleanerRunSummary(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"cleanup_after": "1s"})
	store := openStore(t)
	insertAbsent(t, store, filepath.Join(tmp, "src", "gone.tar"), "2020-06-01T00:00:00Z", nil)
	if err := os.MkdirAll(filepath.Join(tmp, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, store).Run()
	if len(result.PurgedIDs()) != 1 {
		t.Fatalf("purged=%v errors=%v", result.PurgedIDs(), result.Errors)
	}
}

func TestRemoveUnusedMountDir(t *testing.T) {
	tmp := t.TempDir()
	orphan := filepath.Join(tmp, "mounts", "old.tgz")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if !cleaner.RemoveUnusedMountDir(orphan, nil, filepath.Join(tmp, "mounts")) {
		t.Fatal("should remove empty dir")
	}
	if dirExists(orphan) {
		t.Fatal("gone")
	}
}

func TestCleanupStaleMountDirsKeepsProtected(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	store := openStore(t)
	mount := filepath.Join(tmp, "mounts", "active.7z")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Join(tmp, "src"),
		ArchivePath:     filepath.Join(tmp, "src", "active.7z"),
		ArchiveBasename: "active.7z",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
		MountPath:       &mount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(tmp, "mounts", "stale.tgz")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	removed := cleaner.CleanupStaleMountDirs(c, store, nil, nil)
	found := false
	for _, p := range removed {
		if p == orphan {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan not in removed: %v", removed)
	}
	if !dirExists(mount) {
		t.Fatal("protected mount path must remain")
	}
	if dirExists(orphan) {
		t.Fatal("orphan should be gone")
	}
}

func TestPruneStaleMountDirsInRun(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	store := openStore(t)
	stale := filepath.Join(tmp, "mounts", "leftover.tgz")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, store).Run()
	found := false
	for _, p := range result.MountDirsRemoved {
		if p == stale {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale not removed: %v", result.MountDirsRemoved)
	}
}

func TestIsRatarmountTempPath(t *testing.T) {
	tmp := t.TempDir()
	if cleaner.IsRatarmountTempPath(filepath.Join(tmp, ".tmpabc")) {
		t.Fatal("missing file is not a temp")
	}
	f := filepath.Join(tmp, ".tmpabc")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cleaner.IsRatarmountTempPath(f) {
		t.Fatal("expected temp")
	}
	if cleaner.IsRatarmountTempPath(filepath.Join(tmp, "not-tmp")) {
		t.Fatal("non .tmp name")
	}
}

func TestPruneOrphanRatarmountTemps(t *testing.T) {
	tmp := t.TempDir()
	orphan := filepath.Join(tmp, ".tmporphan")
	if err := os.WriteFile(orphan, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(tmp, ".tmporphan.index.sqlite")
	if err := os.WriteFile(sidecar, []byte("idx"), 0o644); err != nil {
		t.Fatal(err)
	}
	busy := filepath.Join(tmp, ".tmpbusy")
	if err := os.WriteFile(busy, make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(tmp, "keep.txt")
	if err := os.WriteFile(keep, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, freed := cleaner.PruneOrphanRatarmountTemps(tmp, func(path string) bool {
		return path == busy
	})
	if removed != 2 {
		t.Fatalf("removed=%d", removed)
	}
	if freed != 100+3 {
		t.Fatalf("freed=%d", freed)
	}
	if fileExists(orphan) || fileExists(sidecar) {
		t.Fatal("orphans should be gone")
	}
	if !fileExists(busy) || !fileExists(keep) {
		t.Fatal("busy/keep must remain")
	}
}

func TestCleanerTmpDirDefaultDoesNotTouchLocalTemps(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, nil)
	orphan := filepath.Join(tmp, ".tmpdead")
	if err := os.WriteFile(orphan, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	// Default TmpDir is /tmp — local orphan stays.
	removed, freed := cleaner.New(c, openStore(t)).PruneOrphanRatarmountTemps()
	if removed != 0 || freed != 0 {
		t.Fatalf("removed=%d freed=%d", removed, freed)
	}
	if !fileExists(orphan) {
		t.Fatal("local orphan must remain")
	}

	removed, freed = cleaner.PruneOrphanRatarmountTemps(tmp, func(string) bool { return false })
	if removed != 1 || freed != 10 {
		t.Fatalf("removed=%d freed=%d", removed, freed)
	}
}

func TestCheckDiskLow(t *testing.T) {
	tmp := t.TempDir()
	c := cfg(t, tmp, map[string]any{"min_free_bytes": 1 << 60}) // absurdly high
	if err := os.MkdirAll(filepath.Join(tmp, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	cl := cleaner.New(c, openStore(t))
	low, free := cl.CheckDisk()
	if free == nil {
		t.Skip("free space probe unavailable")
	}
	if !low {
		t.Fatalf("expected low disk free=%d", *free)
	}
}

// ---------------------------------------------------------------------------
// Outer nonsolid cache hygiene
// ---------------------------------------------------------------------------

func TestPruneNonsolidCachePartials(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(cache, "abc.7z.nonsolid.partial")
	if err := os.WriteFile(partial, []byte("partial-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(cache, "abc.7z.nonsolid.partial.work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "x"), []byte("w"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Keep a young .7z so we only exercise partial cleanup here.
	young := filepath.Join(cache, "abc.7z")
	if err := os.WriteFile(young, []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := cleaner.PruneNonsolidCache(cache, 24*time.Hour, time.Now().UTC(), nil)
	if res.PartialsRemoved < 2 {
		t.Fatalf("partials removed=%d want >=2: %+v", res.PartialsRemoved, res)
	}
	if fileExists(partial) {
		t.Fatal("partial should be gone")
	}
	if dirExists(work) {
		t.Fatal("work dir should be gone")
	}
	if !fileExists(young) {
		t.Fatal("young .7z must remain")
	}
}

func TestPruneNonsolidCacheYoungKeptOldOrphanPruned(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	young := filepath.Join(cache, "young.7z")
	if err := os.WriteFile(young, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(cache, "oldkey.7z")
	if err := os.WriteFile(old, []byte("old-archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldMeta := old + ".tarmount-convert.json"
	if err := os.WriteFile(oldMeta, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLock := filepath.Join(cache, "oldkey.lock")
	if err := os.WriteFile(oldLock, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := time.Now().UTC().Add(-48 * time.Hour)
	for _, p := range []string{old, oldMeta, oldLock} {
		if err := os.Chtimes(p, oldTS, oldTS); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	res := cleaner.PruneNonsolidCache(cache, 24*time.Hour, now, nil)
	if res.ArchivesRemoved != 1 {
		t.Fatalf("archives removed=%d want 1: %+v", res.ArchivesRemoved, res)
	}
	if fileExists(old) {
		t.Fatal("old orphan .7z should be pruned")
	}
	if fileExists(oldMeta) {
		t.Fatal("sidecar should be pruned with orphan")
	}
	if fileExists(oldLock) {
		t.Fatal("lock should be pruned with orphan")
	}
	if !fileExists(young) {
		t.Fatal("young .7z must remain")
	}
	if res.BytesFreed < int64(len("old-archive")) {
		t.Fatalf("bytes freed=%d", res.BytesFreed)
	}
}

func TestPruneNonsolidCacheStaleLockWithout7z(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Lock with no sibling .7z → stale.
	staleLock := filepath.Join(cache, "deadbeef.lock")
	if err := os.WriteFile(staleLock, []byte("L"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Lock with sibling .7z → keep.
	keep7z := filepath.Join(cache, "alive.7z")
	if err := os.WriteFile(keep7z, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	keepLock := filepath.Join(cache, "alive.lock")
	if err := os.WriteFile(keepLock, []byte("L"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := cleaner.PruneNonsolidCache(cache, 24*time.Hour, time.Now().UTC(), nil)
	if res.LocksRemoved < 1 {
		t.Fatalf("expected stale lock removed: %+v", res)
	}
	if fileExists(staleLock) {
		t.Fatal("stale lock should be gone")
	}
	if !fileExists(keepLock) || !fileExists(keep7z) {
		t.Fatal("live pair must remain")
	}
}

func TestPruneNonsolidCachePathOutsideRefused(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// File that lives outside the cache root — PruneNonsolidCache only lists
	// direct children of cacheDir, so outside is never a candidate.
	outsider := filepath.Join(outside, "escape.7z")
	if err := os.WriteFile(outsider, []byte("do-not-delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := time.Now().UTC().Add(-72 * time.Hour)
	if err := os.Chtimes(outsider, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}

	// Empty cacheDir string / missing dir are no-ops.
	resEmpty := cleaner.PruneNonsolidCache("", 1*time.Hour, time.Now().UTC(), nil)
	if resEmpty.ArchivesRemoved != 0 || resEmpty.PartialsRemoved != 0 {
		t.Fatalf("empty cacheDir must no-op: %+v", resEmpty)
	}
	resMissing := cleaner.PruneNonsolidCache(filepath.Join(tmp, "no-such"), 1*time.Hour, time.Now().UTC(), nil)
	if resMissing.ArchivesRemoved != 0 {
		t.Fatalf("missing dir must no-op: %+v", resMissing)
	}

	// Pruning cache must not touch outside.
	_ = cleaner.PruneNonsolidCache(cache, 1*time.Hour, time.Now().UTC(), nil)
	if !fileExists(outsider) {
		t.Fatal("path outside cache root must not be deleted")
	}
}

func TestPruneNonsolidCacheLivePathSkipped(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(cache, "inuse.7z")
	if err := os.WriteFile(live, []byte("mounted"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTS := time.Now().UTC().Add(-72 * time.Hour)
	if err := os.Chtimes(live, oldTS, oldTS); err != nil {
		t.Fatal(err)
	}
	res := cleaner.PruneNonsolidCache(cache, 1*time.Hour, time.Now().UTC(), []string{live})
	if res.ArchivesRemoved != 0 {
		t.Fatalf("live path must not age-prune: %+v", res)
	}
	if !fileExists(live) {
		t.Fatal("live .7z must remain")
	}
}

func TestPruneNonsolidCacheWiredInRun(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "ns-cache")
	c := cfg(t, tmp, map[string]any{
		"cleanup_after":       "1h",
		"convert_7z_cache_dir": cache,
	})
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(cache, "x.7z.nonsolid.partial")
	if err := os.WriteFile(partial, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := cleaner.New(c, openStore(t)).Run()
	if result.NonsolidPartialsRemoved < 1 {
		t.Fatalf("Run should prune nonsolid partials: %+v", result)
	}
	if fileExists(partial) {
		t.Fatal("partial should be gone after Run")
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
