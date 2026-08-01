package reconcile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/hooks"
	"github.com/hilather/mount-wrapper/internal/reconcile"
	"github.com/hilather/mount-wrapper/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertArchive(t *testing.T, store *state.Store, path string) *state.ArchiveRecord {
	t.Helper()
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Dir(path),
		ArchivePath:     path,
		ArchiveBasename: filepath.Base(path),
		SizeBytes:       10,
		MtimeNs:         1,
		Fingerprint:     "10:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func settings() reconcile.Settings {
	return reconcile.Settings{
		MountReadyTimeoutSeconds: 3600,
		MaxMountAttempts:         10,
	}
}

func TestIndexingWithoutIsMountIsOK(t *testing.T) {
	// Long first index: not ismount must NOT fail while PID is alive.
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	mnt := filepath.Join(tmp, "mnt")
	now := state.UTCNowISO()
	pid := int64(os.Getpid())
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid":        pid,
		"mount_path":       mnt,
		"index_started_at": now,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return false },
		PIDAlive:   func(int) bool { return true },
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	a := result.ActionFor(rec.ArchiveID)
	if a == nil || a.Kind != reconcile.ActionOK {
		t.Fatalf("want ok, got %+v", a)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusIndexing {
		t.Fatalf("status=%s want indexing", rec2.Status)
	}
}

func TestIndexingDeadPIDFails(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(tmp, "partial.index.sqlite")
	if err := os.WriteFile(idx, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid":        int64(999_999_999),
		"mount_path":       filepath.Join(tmp, "mnt"),
		"index_path":       idx,
		"index_started_at": state.UTCNowISO(),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return false },
		PIDAlive:   func(int) bool { return false },
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionFailIndex) {
		t.Fatalf("want fail_index, actions=%+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusIndexFailed {
		t.Fatalf("status=%s want index_failed", rec2.Status)
	}
	if _, err := os.Stat(idx); !os.IsNotExist(err) {
		t.Fatalf("partial index should be deleted: %v", err)
	}
	if rec2.IndexPath != nil {
		t.Fatalf("index_path should be cleared, got %v", *rec2.IndexPath)
	}
	if rec2.MountPID != nil {
		t.Fatalf("mount_pid should be nil")
	}
}

func TestMountedRequiresIsMountAndPID(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	mnt := filepath.Join(tmp, "mnt")
	pid := int64(os.Getpid())
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mnt,
		"mount_pid":  pid,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": mnt,
		"mount_pid":  pid,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return false }, // external unmount
		PIDAlive:   func(int) bool { return true },
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionFailMount) {
		t.Fatalf("want fail_mount, actions=%+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s want mount_failed", rec2.Status)
	}
	// hooks not re-run (status not hooks_running reset)
	if rec2.HooksStatus != state.HooksNone {
		t.Fatalf("hooks_status=%s want none", rec2.HooksStatus)
	}
	if hooks.ShouldRunHooks(rec2.HooksStatus, false) {
		// none is eligible for first-run; important is success stays terminal.
	}
}

func TestMountedUnhealthyPreservesHooksSuccess(t *testing.T) {
	// Never re-run terminal-success hooks on remount path.
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	mnt := filepath.Join(tmp, "mnt")
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mnt,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path":   mnt,
		"mount_pid":    int64(os.Getpid()),
		"hooks_status": state.HooksSuccess,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return false },
		PIDAlive:   func(int) bool { return false },
		PathExists: func(string) bool { return true },
	})
	if _, err := recon.Reconcile(); err != nil {
		t.Fatal(err)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s", rec2.Status)
	}
	if rec2.HooksStatus != state.HooksSuccess {
		t.Fatalf("hooks_status=%s want success preserved", rec2.HooksStatus)
	}
	if hooks.ShouldRunHooks(rec2.HooksStatus, false) {
		t.Fatal("ShouldRunHooks must be false for terminal success after remount fail")
	}
}

func TestMountedHealthy(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	mnt := filepath.Join(tmp, "mnt")
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mnt,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": mnt,
		"mount_pid":  int64(os.Getpid()),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return true },
		PIDAlive:   func(int) bool { return true },
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	a := result.ActionFor(rec.ArchiveID)
	if a == nil || a.Kind != reconcile.ActionOK {
		t.Fatalf("want ok, got %+v", a)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMounted {
		t.Fatalf("status=%s", rec2.Status)
	}
}

func TestUnhealthyAndMissingArchiveMarksAbsent(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	// Path never written — missing archive.
	rec := insertArchive(t, store, filepath.Join(tmp, "gone.tar.gz"))
	mnt := filepath.Join(tmp, "mnt")
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mnt,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": mnt,
		"mount_pid":  int64(os.Getpid()),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		IsMount:    func(string) bool { return false },
		PIDAlive:   func(int) bool { return false },
		PathExists: func(string) bool { return false },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionMarkAbsent) {
		t.Fatalf("want mark_absent, actions=%+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusAbsent {
		t.Fatalf("status=%s want absent", rec2.Status)
	}
}

func TestMountReadyTimeoutFailsIndex(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	// Started far in the past.
	started := time.Unix(1_000_000, 0).UTC().Format(time.RFC3339)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid":        int64(os.Getpid()),
		"index_started_at": started,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, reconcile.Settings{
		MountReadyTimeoutSeconds: 60,
		MaxMountAttempts:         10,
	}).WithProbes(reconcile.Probes{
		IsMount:  func(string) bool { return false },
		PIDAlive: func(int) bool { return true },
		Clock:    func() float64 { return 1_000_000 + 120 }, // 120s later
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionFailIndex) {
		t.Fatalf("want fail_index on timeout, got %+v", result.Actions)
	}
}

func TestConvertingWithoutJobFails(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.7z")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusConverting, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		ConvertActive: func(string) bool { return false },
		PathExists:    func(string) bool { return true },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionFailMount) {
		t.Fatalf("want fail_mount, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s", rec2.Status)
	}
}

func TestConvertingWithActiveJobOK(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.7z")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusConverting, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		ConvertActive: func(id string) bool { return id == rec.ArchiveID },
	})
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	a := result.ActionFor(rec.ArchiveID)
	if a == nil || a.Kind != reconcile.ActionOK {
		t.Fatalf("want ok, got %+v", a)
	}
}

func TestPartialIndexCleanupAction(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(tmp, "partial.index.sqlite")
	if err := os.WriteFile(idx, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	// discovered + index_path + no first_mounted_at → cleanup
	if _, err := store.Transition(rec.ArchiveID, state.StatusDiscovered, state.StatusDiscovered, map[string]any{
		"index_path": idx,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings())
	result, err := recon.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionCleanupIndex) {
		t.Fatalf("want cleanup_index, got %+v", result.Actions)
	}
	if _, err := os.Stat(idx); !os.IsNotExist(err) {
		t.Fatal("index should be deleted")
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.IndexPath != nil {
		t.Fatal("index_path should be nil")
	}
}

func TestDecideOneTable(t *testing.T) {
	// Pure decision matrix without store apply.
	pid := int64(42)
	mnt := "/mnt/x"
	archive := "/src/a.tar.gz"
	idx := "/idx/a.sqlite"
	nowISO := time.Now().UTC().Format(time.RFC3339)

	cases := []struct {
		name    string
		rec     state.ArchiveRecord
		probes  reconcile.Probes
		set     reconcile.Settings
		want    reconcile.ActionKind
		wantNil bool
	}{
		{
			name:    "absent skipped",
			rec:     state.ArchiveRecord{ArchiveID: "1", Status: state.StatusAbsent, ArchivePath: archive},
			wantNil: true,
		},
		{
			name: "indexing alive ok",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusIndexing, ArchivePath: archive,
				MountPID: &pid, MountPath: &mnt, IndexStartedAt: &nowISO,
			},
			probes: reconcile.Probes{
				IsMount: func(string) bool { return false }, PIDAlive: func(int) bool { return true },
				Clock: func() float64 { return float64(time.Now().Unix()) },
			},
			set:  reconcile.Settings{MountReadyTimeoutSeconds: 3600, MaxMountAttempts: 10},
			want: reconcile.ActionOK,
		},
		{
			name: "mounting dead pid fail_mount",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusMounting, ArchivePath: archive,
				MountPID: &pid, IndexStartedAt: &nowISO,
			},
			probes: reconcile.Probes{PIDAlive: func(int) bool { return false }, Clock: func() float64 { return 0 }},
			set:    reconcile.Settings{MountReadyTimeoutSeconds: 3600, MaxMountAttempts: 10},
			want:   reconcile.ActionFailMount,
		},
		{
			name: "hooks_running healthy ok",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusHooksRunning, ArchivePath: archive,
				MountPID: &pid, MountPath: &mnt,
			},
			probes: reconcile.Probes{
				IsMount: func(string) bool { return true }, PIDAlive: func(int) bool { return true },
				PathExists: func(string) bool { return true },
			},
			want: reconcile.ActionOK,
		},
		{
			name: "unmounting present nil",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusUnmounting, ArchivePath: archive,
			},
			probes:  reconcile.Probes{PathExists: func(string) bool { return true }},
			wantNil: true,
		},
		{
			name: "unmounting missing mark_absent",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusUnmounting, ArchivePath: archive,
			},
			probes: reconcile.Probes{PathExists: func(string) bool { return false }},
			want:   reconcile.ActionMarkAbsent,
		},
		{
			name: "discovered partial index",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusDiscovered, ArchivePath: archive,
				IndexPath: &idx,
			},
			probes: reconcile.Probes{IndexIsFile: func(string) bool { return true }},
			want:   reconcile.ActionCleanupIndex,
		},
		{
			name: "discovered no index nil",
			rec: state.ArchiveRecord{
				ArchiveID: "1", Status: state.StatusDiscovered, ArchivePath: archive,
			},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := reconcile.DecideOne(&tc.rec, tc.set, tc.probes)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("want action, got nil")
			}
			if got.Kind != tc.want {
				t.Fatalf("kind=%s want %s", got.Kind, tc.want)
			}
		})
	}
}

func TestBootRemountMounted(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": filepath.Join(tmp, "mnt"),
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_pid":    int64(12345),
		"hooks_status": state.HooksSuccess,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionRequestRemount) {
		t.Fatalf("want request_remount, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s want mount_failed", rec2.Status)
	}
	if rec2.MountPID != nil {
		t.Fatal("stale PID should be cleared")
	}
	if !rec2.MountRetryable {
		t.Fatal("mount_retryable should be true")
	}
	if rec2.HooksStatus != state.HooksSuccess {
		t.Fatal("hooks_status must remain success (no re-run on remount)")
	}
	if hooks.ShouldRunHooks(rec2.HooksStatus, false) {
		t.Fatal("ShouldRunHooks false for success")
	}
}

func TestBootRequeueIndexingNeverMounted(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid": int64(99),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings())
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionRequeue) {
		t.Fatalf("want requeue, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusDiscovered {
		t.Fatalf("status=%s want discovered", rec2.Status)
	}
	if rec2.MountPID != nil {
		t.Fatal("pid cleared")
	}
}

func TestBootRequeueIndexingAfterFirstMount(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	// Reach mounted once so first_mounted_at is set, then back to indexing isn't
	// a normal path — set first_mounted_at via mount then force indexing fields.
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_pid": int64(1),
	}, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate crashed remount indexing: mounted → mounting isn't boot state;
	// transition mounted → mounting then we need indexing. Use mounting for boot.
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusMounted, map[string]any{
		"mount_pid": int64(99),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings())
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionRequeue) {
		t.Fatalf("want requeue, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s want mount_failed (had first_mounted_at)", rec2.Status)
	}
}

func TestBootUnmountingRequeues(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "stuck.7z")
	if err := os.WriteFile(archive, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusUnmounting, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		PathExists: func(string) bool { return true },
	})
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionRequeue) {
		t.Fatalf("want requeue, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusDiscovered {
		t.Fatalf("status=%s want discovered", rec2.Status)
	}
	if !rec2.MountRetryable {
		t.Fatal("mount_retryable true")
	}
}

func TestBootMountedMissingArchive(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	rec := insertArchive(t, store, filepath.Join(tmp, "gone.tar.gz"))
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_pid": int64(1),
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		PathExists: func(string) bool { return false },
	})
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionMarkAbsent) {
		t.Fatalf("want mark_absent, got %+v", result.Actions)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusAbsent {
		t.Fatalf("status=%s", rec2.Status)
	}
}

func TestCleanupPartialIndexes(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(tmp, "partial.index.sqlite")
	if err := os.WriteFile(idx, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusDiscovered, state.StatusDiscovered, map[string]any{
		"index_path": idx,
	}, ""); err != nil {
		t.Fatal(err)
	}

	recon := reconcile.NewWithSettings(store, settings())
	n, err := recon.CleanupPartialIndexes()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
	if _, err := os.Stat(idx); !os.IsNotExist(err) {
		t.Fatal("file gone")
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.IndexPath != nil {
		t.Fatal("index_path cleared")
	}
}

func TestBootMountingAlwaysMountFailed(t *testing.T) {
	// mounting → discovered is illegal; boot must use mount_failed.
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	// discovered → mounting is allowed.
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusDiscovered, map[string]any{
		"mount_pid": int64(7),
	}, ""); err != nil {
		t.Fatal(err)
	}
	// Ensure no first_mounted_at.
	rec, _ = store.GetArchive(rec.ArchiveID)
	if rec.FirstMountedAt != nil {
		t.Fatal("expected no first_mounted_at")
	}

	recon := reconcile.NewWithSettings(store, settings())
	result, err := recon.Boot()
	if err != nil {
		t.Fatal(err)
	}
	a := result.ActionFor(rec.ArchiveID)
	if a == nil || a.Kind != reconcile.ActionRequeue {
		t.Fatalf("want requeue, got %+v", a)
	}
	if a.ApplyError != nil {
		t.Fatalf("apply error: %v", a.ApplyError)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMountFailed {
		t.Fatalf("status=%s want mount_failed", rec2.Status)
	}
}

func TestResultFailures(t *testing.T) {
	r := reconcile.Result{Actions: []reconcile.Action{
		{Kind: reconcile.ActionOK},
		{Kind: reconcile.ActionFailIndex},
		{Kind: reconcile.ActionFailMount},
		{Kind: reconcile.ActionMarkAbsent},
	}}
	f := r.Failures()
	if len(f) != 2 {
		t.Fatalf("failures=%d", len(f))
	}
}

func TestPlanReconcileDoesNotApply(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid":        int64(999_999_999),
		"index_started_at": state.UTCNowISO(),
	}, ""); err != nil {
		t.Fatal(err)
	}
	recon := reconcile.NewWithSettings(store, settings()).WithProbes(reconcile.Probes{
		PIDAlive: func(int) bool { return false },
	})
	result, err := recon.PlanReconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasKind(reconcile.ActionFailIndex) {
		t.Fatal("plan should decide fail_index")
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusIndexing {
		t.Fatalf("plan must not apply; status=%s", rec2.Status)
	}
}

func TestMountAttemptsIncrement(t *testing.T) {
	store := openStore(t)
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_pid":        int64(1),
		"mount_attempts":   2,
		"index_started_at": state.UTCNowISO(),
	}, ""); err != nil {
		t.Fatal(err)
	}
	recon := reconcile.NewWithSettings(store, reconcile.Settings{
		MountReadyTimeoutSeconds: 3600,
		MaxMountAttempts:         10,
	}).WithProbes(reconcile.Probes{PIDAlive: func(int) bool { return false }})
	if _, err := recon.Reconcile(); err != nil {
		t.Fatal(err)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.MountAttempts != 3 {
		t.Fatalf("attempts=%d want 3", rec2.MountAttempts)
	}
	if !rec2.MountRetryable {
		t.Fatal("retryable")
	}
}
