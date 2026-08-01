package state_test

import (
	"errors"
	"path/filepath"
	"testing"

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

func insert(t *testing.T, s *state.Store, overrides map[string]any) *state.ArchiveRecord {
	t.Helper()
	p := state.InsertDiscoveredParams{
		SourceDir:       "/mnt/d/Archives",
		ArchivePath:     "/mnt/d/Archives/sample.tar.gz",
		ArchiveBasename: "sample.tar.gz",
		SizeBytes:       1024,
		MtimeNs:         1_700_000_000_000_000_000,
		Fingerprint:     "1024:1700000000000000000",
	}
	if overrides != nil {
		if v, ok := overrides["source_dir"].(string); ok {
			p.SourceDir = v
		}
		if v, ok := overrides["archive_path"].(string); ok {
			p.ArchivePath = v
		}
		if v, ok := overrides["archive_basename"].(string); ok {
			p.ArchiveBasename = v
		}
		if v, ok := overrides["size_bytes"].(int64); ok {
			p.SizeBytes = v
		}
		if v, ok := overrides["size_bytes"].(int); ok {
			p.SizeBytes = int64(v)
		}
		if v, ok := overrides["mtime_ns"].(int64); ok {
			p.MtimeNs = v
		}
		if v, ok := overrides["fingerprint"].(string); ok {
			p.Fingerprint = v
		}
	}
	rec, err := s.InsertDiscovered(p)
	if err != nil {
		t.Fatalf("InsertDiscovered: %v", err)
	}
	return rec
}

func TestMigrateEmptyDBToVersion6(t *testing.T) {
	s := openStore(t)
	ver, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if ver != state.CurrentSchemaVersion || state.CurrentSchemaVersion != 6 {
		t.Fatalf("schema version = %d, want CurrentSchemaVersion=6", ver)
	}

	needed, err := state.MigrationsNeeded(0, state.CurrentSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(needed) != 6 || needed[0] != 1 || needed[5] != 6 {
		t.Fatalf("MigrationsNeeded(0)=%v, want [1..6]", needed)
	}
	empty, err := state.MigrationsNeeded(6, 6)
	if err != nil || len(empty) != 0 {
		t.Fatalf("MigrationsNeeded(6,6)=%v err=%v", empty, err)
	}

	// Tables present
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('schema_version','archives','hooks','meta')`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("expected 4 core tables, got %d", n)
	}

	// Duration / convert columns usable after full migration
	rec := insert(t, s, map[string]any{"archive_path": "/data/a.tgz", "archive_basename": "a.tgz"})
	updated, err := s.Transition(rec.ArchiveID, rec.Status, rec.Status, map[string]any{
		"index_duration_seconds":    12.5,
		"mount_duration_seconds":    3.25,
		"convert_source_size_bytes": int64(123456789),
		"convert_duration_seconds":  3600.5,
	}, "")
	if err != nil {
		t.Fatalf("Transition fields: %v", err)
	}
	if updated.IndexDurationSeconds == nil || *updated.IndexDurationSeconds != 12.5 {
		t.Fatalf("index_duration_seconds=%v", updated.IndexDurationSeconds)
	}
	if updated.MountDurationSeconds == nil || *updated.MountDurationSeconds != 3.25 {
		t.Fatalf("mount_duration_seconds=%v", updated.MountDurationSeconds)
	}
	if updated.ConvertSourceSizeBytes == nil || *updated.ConvertSourceSizeBytes != 123456789 {
		t.Fatalf("convert_source_size_bytes=%v", updated.ConvertSourceSizeBytes)
	}
	if updated.ConvertDurationSeconds == nil || *updated.ConvertDurationSeconds != 3600.5 {
		t.Fatalf("convert_duration_seconds=%v", updated.ConvertDurationSeconds)
	}

	// converting status allowed by CHECK after 004
	claimed, err := s.ClaimConverting(rec.ArchiveID, nil, "")
	if err != nil {
		t.Fatalf("ClaimConverting: %v", err)
	}
	if claimed.Status != state.StatusConverting {
		t.Fatalf("status=%q", claimed.Status)
	}
}

func TestOpenFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	s1, err := state.Open(db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	insert(t, s1, nil)
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := state.Open(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	list, err := s2.ListArchives(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	ver, _ := s2.SchemaVersion()
	if ver != 6 {
		t.Fatalf("ver=%d", ver)
	}
}

func TestForeignKeysOn(t *testing.T) {
	s := openStore(t)
	var v int
	if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("foreign_keys=%d", v)
	}
}

func TestIllegalTransitionRejected(t *testing.T) {
	if err := state.ValidateTransition(state.StatusAbsent, state.StatusMounted); err == nil {
		t.Fatal("expected error")
	} else if !errors.Is(err, state.ErrTransition) {
		t.Fatalf("want ErrTransition, got %v", err)
	}
	if err := state.ValidateTransition(state.StatusDiscovered, state.StatusMounted); err == nil {
		t.Fatal("expected error")
	}
	// same → same ok
	if err := state.ValidateTransition(state.StatusMounted, state.StatusMounted); err != nil {
		t.Fatal(err)
	}
	// no purged status
	if err := state.ValidateTransition(state.StatusAbsent, "purged"); err == nil {
		t.Fatal("expected unknown to_status")
	}

	s := openStore(t)
	rec := insert(t, s, nil)
	_, err := s.Transition(rec.ArchiveID, state.StatusMounted, state.StatusDiscovered, nil, "")
	if err == nil {
		t.Fatal("expected illegal transition")
	}
	if !errors.Is(err, state.ErrTransition) {
		t.Fatalf("errors.Is Transition: %v", err)
	}
}

func TestAllDeclaredEdgesValid(t *testing.T) {
	for src, dests := range state.ALLOWED_TRANSITIONS {
		if !state.IsArchiveStatus(src) {
			t.Fatalf("unknown src %q", src)
		}
		for dest := range dests {
			if err := state.ValidateTransition(src, dest); err != nil {
				t.Fatalf("%s→%s: %v", src, dest, err)
			}
		}
	}
}

func TestClaimIndexingOptimisticLock(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	idx := "/idx/1.sqlite"
	claimed, err := s.ClaimIndexing(rec.ArchiveID, map[string]any{"index_path": idx}, "")
	if err != nil {
		t.Fatalf("ClaimIndexing: %v", err)
	}
	if claimed.Status != state.StatusIndexing {
		t.Fatalf("status=%q", claimed.Status)
	}
	if claimed.IndexPath == nil || *claimed.IndexPath != idx {
		t.Fatalf("index_path=%v", claimed.IndexPath)
	}
	if claimed.IndexStartedAt == nil {
		t.Fatal("index_started_at unset")
	}

	_, err = s.ClaimIndexing(rec.ArchiveID, nil, "")
	if err == nil {
		t.Fatal("expected optimistic lock miss")
	}
	if !errors.Is(err, state.ErrTransition) {
		t.Fatalf("want ErrTransition, got %v", err)
	}
	if msg := err.Error(); !contains(msg, "optimistic lock") {
		t.Fatalf("msg=%q", msg)
	}
}

func TestClaimConverting(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	claimed, err := s.ClaimConverting(rec.ArchiveID, nil, "")
	if err != nil {
		t.Fatalf("ClaimConverting: %v", err)
	}
	if claimed.Status != state.StatusConverting {
		t.Fatalf("status=%q", claimed.Status)
	}
	// second claim fails optimistic lock
	_, err = s.ClaimConverting(rec.ArchiveID, nil, "")
	if err == nil || !errors.Is(err, state.ErrTransition) {
		t.Fatalf("expected lock miss, got %v", err)
	}
}

func TestIndexingToMountedSetsFirstMountedAt(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	mounted, err := s.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": "/mnt/point",
		"mount_pid":  int64(1234),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if mounted.FirstMountedAt == nil {
		t.Fatal("first_mounted_at unset")
	}
	if mounted.MountPID == nil || *mounted.MountPID != 1234 {
		t.Fatalf("mount_pid=%v", mounted.MountPID)
	}
}

func TestPurgeCascadesHooks(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.SeedHooks(rec.ArchiveID, []string{"10-list.sh"}, ""); err != nil {
		t.Fatal(err)
	}
	hooks, err := s.ListHooks(rec.ArchiveID)
	if err != nil || len(hooks) != 1 {
		t.Fatalf("hooks=%v err=%v", hooks, err)
	}
	if err := s.PurgeArchive(rec.ArchiveID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetArchive(rec.ArchiveID)
	if err != nil || got != nil {
		t.Fatalf("archive still present: %v %v", got, err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM hooks WHERE archive_id = ?`, rec.ArchiveID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("hooks remaining=%d", n)
	}
}

func TestPurgeFreesPath(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, map[string]any{"archive_path": "/mnt/d/Archives/gone.tar.gz", "archive_basename": "gone.tar.gz"})
	path := rec.ArchivePath
	if _, err := s.MarkAbsent(rec.ArchiveID, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeArchive(rec.ArchiveID); err != nil {
		t.Fatal(err)
	}
	again, err := s.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       "/mnt/d/Archives",
		ArchivePath:     path,
		ArchiveBasename: "gone.tar.gz",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ArchiveID == rec.ArchiveID {
		t.Fatal("expected new archive_id")
	}
}

func TestPurgeMissing(t *testing.T) {
	s := openStore(t)
	err := s.PurgeArchive(state.NewArchiveID())
	if err == nil || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMarkAbsentTwoStep(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_pid": int64(1),
	}, ""); err != nil {
		t.Fatal(err)
	}
	absent, err := s.MarkAbsent(rec.ArchiveID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Status != state.StatusAbsent {
		t.Fatalf("status=%q", absent.Status)
	}
	if absent.RemovedAt == nil {
		t.Fatal("removed_at unset")
	}
	if absent.MountPID != nil {
		t.Fatalf("mount_pid=%v", absent.MountPID)
	}
}

func TestReappearAbsentToDiscovered(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.MarkAbsent(rec.ArchiveID, "", nil); err != nil {
		t.Fatal(err)
	}
	back, err := s.Reappear(rec.ArchiveID, 2048, 99, "2048:99", "")
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != state.StatusDiscovered {
		t.Fatalf("status=%q", back.Status)
	}
	if back.RemovedAt != nil {
		t.Fatalf("removed_at=%v", back.RemovedAt)
	}
	if back.SizeBytes != 2048 {
		t.Fatalf("size=%d", back.SizeBytes)
	}
}

func TestRecordContentChangeResetVsKeepHooks(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ArchiveID, state.StatusHooksRunning, state.StatusIndexing, map[string]any{
		"hooks_status": state.HooksRunning,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedHooks(rec.ArchiveID, []string{"10-a.sh", "20-b.sh"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ArchiveID, state.StatusMounted, state.StatusHooksRunning, map[string]any{
		"hooks_status":       state.HooksSuccess,
		"hooks_completed_at": state.UTCNowISO(),
	}, ""); err != nil {
		t.Fatal(err)
	}
	hooks, _ := s.ListHooks(rec.ArchiveID)
	if len(hooks) != 2 {
		t.Fatalf("hooks=%d", len(hooks))
	}

	// reset_hooks=true: delete hook rows, clear hooks_status / first_mounted_at
	changed, err := s.RecordContentChange(rec.ArchiveID, 9, 9, "9:9", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Status != state.StatusDiscovered {
		t.Fatalf("status=%q", changed.Status)
	}
	if changed.HooksStatus != state.HooksNone {
		t.Fatalf("hooks_status=%q", changed.HooksStatus)
	}
	if changed.HooksCompletedAt != nil {
		t.Fatal("hooks_completed_at should be nil")
	}
	if changed.FirstMountedAt != nil {
		t.Fatal("first_mounted_at should be nil")
	}
	hooks, _ = s.ListHooks(rec.ArchiveID)
	if len(hooks) != 0 {
		t.Fatalf("hooks remaining=%d", len(hooks))
	}

	// rebuild mounted + hooks, keep hooks
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"hooks_status": state.HooksSuccess,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedHooks(rec.ArchiveID, []string{"10-a.sh"}, ""); err != nil {
		t.Fatal(err)
	}
	// note first_mounted_at should be set from indexing→mounted
	before, _ := s.GetArchive(rec.ArchiveID)
	kept, err := s.RecordContentChange(rec.ArchiveID, 10, 10, "10:10", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status != state.StatusDiscovered {
		t.Fatalf("status=%q", kept.Status)
	}
	// hooks rows preserved when reset_hooks=false
	hooks, _ = s.ListHooks(rec.ArchiveID)
	if len(hooks) != 1 {
		t.Fatalf("hooks=%d want 1", len(hooks))
	}
	// first_mounted_at preserved when not resetting hooks
	if before.FirstMountedAt != nil {
		if kept.FirstMountedAt == nil || *kept.FirstMountedAt != *before.FirstMountedAt {
			t.Fatalf("first_mounted_at not kept: before=%v after=%v", before.FirstMountedAt, kept.FirstMountedAt)
		}
	}
}

func TestInsertAndGetByPath(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if rec.Status != state.StatusDiscovered {
		t.Fatalf("status=%q", rec.Status)
	}
	if !rec.MountRetryable || rec.MountAttempts != 0 {
		t.Fatalf("defaults: retryable=%v attempts=%d", rec.MountRetryable, rec.MountAttempts)
	}
	got, err := s.GetArchiveByPath(rec.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ArchiveID != rec.ArchiveID {
		t.Fatalf("got=%v", got)
	}
	missing, err := s.GetArchiveByPath("/nope")
	if err != nil || missing != nil {
		t.Fatalf("missing=%v err=%v", missing, err)
	}

	// unique path
	_, err = s.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       rec.SourceDir,
		ArchivePath:     rec.ArchivePath,
		ArchiveBasename: rec.ArchiveBasename,
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	})
	if err == nil || !errors.Is(err, state.ErrState) {
		t.Fatalf("expected StateError on duplicate path, got %v", err)
	}
}

func TestListByStatus(t *testing.T) {
	s := openStore(t)
	a := insert(t, s, map[string]any{"archive_path": "/mnt/d/a.tar", "archive_basename": "a.tar"})
	b := insert(t, s, map[string]any{"archive_path": "/mnt/d/b.tar", "archive_basename": "b.tar"})
	if _, err := s.ClaimIndexing(b.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	disc, err := s.ListArchives(state.StatusDiscovered)
	if err != nil {
		t.Fatal(err)
	}
	if len(disc) != 1 || disc[0].ArchiveID != a.ArchiveID {
		t.Fatalf("discovered=%v", ids(disc))
	}
	both, err := s.ListArchives([]string{state.StatusDiscovered, state.StatusIndexing})
	if err != nil || len(both) != 2 {
		t.Fatalf("both=%v err=%v", ids(both), err)
	}
	empty, err := s.ListArchives([]string{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty filter: %v %v", empty, err)
	}
}

func TestFailAndRetry(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Transition(rec.ArchiveID, state.StatusIndexFailed, state.StatusIndexing, map[string]any{
		"last_error":     "boom",
		"mount_attempts": 1,
		"mount_pid":      nil,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != state.StatusIndexFailed {
		t.Fatalf("status=%q", failed.Status)
	}
	reset, err := s.ResetMountAttempts(rec.ArchiveID, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if reset.Status != state.StatusDiscovered || reset.MountAttempts != 0 || !reset.MountRetryable {
		t.Fatalf("reset=%+v", reset)
	}
	if reset.LastError != nil {
		t.Fatalf("last_error=%v", reset.LastError)
	}
}

func TestTouchSeen(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	size := int64(111)
	touched, err := s.TouchSeen(rec.ArchiveID, &size, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if touched.Status != state.StatusDiscovered || touched.SizeBytes != 111 {
		t.Fatalf("touched=%+v", touched)
	}
}

func TestListAbsentPastGrace(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.MarkAbsent(rec.ArchiveID, "2020-01-01T00:00:00Z", nil); err != nil {
		t.Fatal(err)
	}
	past, err := s.ListAbsentPastGrace("2020-01-02T00:00:00Z")
	if err != nil || len(past) != 1 {
		t.Fatalf("past=%v err=%v", past, err)
	}
	future, err := s.ListAbsentPastGrace("2019-01-01T00:00:00Z")
	if err != nil || len(future) != 0 {
		t.Fatalf("future=%v err=%v", future, err)
	}
}

func TestResetAllPresentAttempts(t *testing.T) {
	s := openStore(t)
	a := insert(t, s, map[string]any{"archive_path": "/mnt/d/a.tar", "archive_basename": "a.tar"})
	b := insert(t, s, map[string]any{"archive_path": "/mnt/d/b.tar", "archive_basename": "b.tar"})
	if _, err := s.ClaimIndexing(a.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(a.ArchiveID, state.StatusIndexFailed, state.StatusIndexing, map[string]any{
		"mount_attempts":  10,
		"mount_retryable": false,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkAbsent(b.ArchiveID, "", nil); err != nil {
		t.Fatal(err)
	}
	n, err := s.ResetAllPresentAttempts("")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	a2, _ := s.GetArchive(a.ArchiveID)
	if a2.MountAttempts != 0 || !a2.MountRetryable {
		t.Fatalf("a2=%+v", a2)
	}
	b2, _ := s.GetArchive(b.ArchiveID)
	if b2.Status != state.StatusAbsent {
		t.Fatalf("b2 status=%q", b2.Status)
	}
}

func TestMetaAndTxn(t *testing.T) {
	s := openStore(t)
	v, err := s.GetMeta("last_scan")
	if err != nil || v != nil {
		t.Fatalf("get empty: %v %v", v, err)
	}
	if err := s.SetMeta("last_scan", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	v, err = s.GetMeta("last_scan")
	if err != nil || v == nil || *v != "2026-01-01T00:00:00Z" {
		t.Fatalf("got %v err %v", v, err)
	}
	if err := s.SetMeta("last_scan", "2026-01-02T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetMeta("last_scan")
	if v == nil || *v != "2026-01-02T00:00:00Z" {
		t.Fatalf("got %v", v)
	}

	rec := insert(t, s, nil)
	err = s.Transaction(func() error {
		if _, e := s.DB().Exec(
			`UPDATE archives SET status = 'indexing' WHERE archive_id = ?`,
			rec.ArchiveID,
		); e != nil {
			return e
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	got, _ := s.GetArchive(rec.ArchiveID)
	if got.Status != state.StatusDiscovered {
		t.Fatalf("status after rollback=%q", got.Status)
	}
}

func TestHooksUpsertAndSeed(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	if _, err := s.SeedHooks(rec.ArchiveID, []string{"10-a.sh", "20-b.sh"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedHooks(rec.ArchiveID, []string{"10-a.sh", "30-c.sh"}, ""); err != nil {
		t.Fatal(err)
	}
	hooks, err := s.ListHooks(rec.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 3 {
		t.Fatalf("names=%v", hookNames(hooks))
	}
	attempts := 1
	exit := 0
	runAt := state.UTCNowISO()
	if _, err := s.UpsertHook(rec.ArchiveID, "10-a.sh", state.UpsertHookParams{
		Status:       state.HookSuccess,
		Attempts:     &attempts,
		LastExitCode: &exit,
		LastRunAt:    &runAt,
	}); err != nil {
		t.Fatal(err)
	}
	hooks, _ = s.ListHooks(rec.ArchiveID)
	var h *state.HookRecord
	for _, x := range hooks {
		if x.HookName == "10-a.sh" {
			h = x
		}
	}
	if h == nil || h.Status != state.HookSuccess || h.Attempts != 1 || h.LastExitCode == nil || *h.LastExitCode != 0 {
		t.Fatalf("hook=%+v", h)
	}
}

func TestNotFoundTransition(t *testing.T) {
	s := openStore(t)
	_, err := s.Transition(state.NewArchiveID(), state.StatusIndexing, nil, nil, "")
	if err == nil || !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMigrationSQLLoads(t *testing.T) {
	sql, err := state.MigrationSQL(1)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sql, "CREATE TABLE archives") || !contains(sql, "hooks_status") || !contains(sql, "ON DELETE CASCADE") {
		t.Fatalf("unexpected sql: %s", sql[:min(200, len(sql))])
	}
}

func TestUTCNowISOFormat(t *testing.T) {
	s := state.UTCNowISO()
	// 2006-01-02T15:04:05Z
	if len(s) != 20 || s[len(s)-1] != 'Z' {
		t.Fatalf("UTCNowISO=%q", s)
	}
	id := state.NewArchiveID()
	if len(id) < 32 {
		t.Fatalf("uuid=%q", id)
	}
}

func TestClaimMountingDefaultExpected(t *testing.T) {
	s := openStore(t)
	rec := insert(t, s, nil)
	// discovered is not in default expected set
	_, err := s.ClaimMounting(rec.ArchiveID, nil, nil, "")
	if err == nil || !errors.Is(err, state.ErrTransition) {
		t.Fatalf("expected lock miss from discovered, got %v", err)
	}
	// via mount_failed
	if _, err := s.ClaimIndexing(rec.ArchiveID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ArchiveID, state.StatusMountFailed, state.StatusIndexing, map[string]any{
		"last_error": "x",
	}, ""); err != nil {
		// indexing → mount_failed is allowed
		t.Fatal(err)
	}
	claimed, err := s.ClaimMounting(rec.ArchiveID, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != state.StatusMounting {
		t.Fatalf("status=%q", claimed.Status)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}

func ids(recs []*state.ArchiveRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ArchiveID
	}
	return out
}

func hookNames(hooks []*state.HookRecord) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.HookName
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
