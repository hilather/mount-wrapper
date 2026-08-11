//go:build unix

package mounter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

func TestParseRatarmountMountPath(t *testing.T) {
	t.Parallel()
	cmdline := []byte("/home/mbrewer/.local/bin/ratarmount\x00-f\x00--index-file\x00/tmp/x.index.sqlite\x00" +
		"/data/archive.7z\x00/mounts/SUP-123.7z\x00")
	got, ok := mounter.TestParseRatarmountMountPath(cmdline)
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "/mounts/SUP-123.7z" {
		t.Fatalf("mount path=%q", got)
	}
}

func TestClearStaleMountHolders_killsOthers(t *testing.T) {
	t.Parallel()
	restore := mounter.SetTestRatarmountProcLister(func() ([]mounter.RatarmountProc, error) {
		return []mounter.RatarmountProc{
			{PID: 100, MountPath: "/mounts/a.7z"},
			{PID: 200, MountPath: "/mounts/a.7z"},
			{PID: 300, MountPath: "/mounts/b.7z"},
		}, nil
	})
	defer restore()

	var killed []int
	restoreKill := mounter.SetTestTerminateOrphanPID(func(pid int) {
		killed = append(killed, pid)
	})
	defer restoreKill()

	killedOut, _ := mounter.ClearStaleMountHolders("/mounts/a.7z", 200, mounter.UnmountOptions{
		IsMount: func(string) bool { return false },
	})
	if len(killedOut) != 1 || killedOut[0] != 100 {
		t.Fatalf("killedOut=%v", killedOut)
	}
	if len(killed) != 1 || killed[0] != 100 {
		t.Fatalf("killed=%v", killed)
	}
}

func TestReconcileOrphanMounts_killsUntracked(t *testing.T) {
	t.Parallel()
	restore := mounter.SetTestRatarmountProcLister(func() ([]mounter.RatarmountProc, error) {
		return []mounter.RatarmountProc{
			{PID: 111, MountPath: "/mounts/tracked.7z"},
			{PID: 222, MountPath: "/mounts/stale.7z"},
		}, nil
	})
	defer restore()

	var killed []int
	restoreKill := mounter.SetTestTerminateOrphanPID(func(pid int) {
		killed = append(killed, pid)
	})
	defer restoreKill()

	mp := "/mounts/tracked.7z"
	pid := int64(111)
	eng := mounter.NewEngine(&config.Config{MountRoot: "/mounts"}, nil)
	res := eng.ReconcileOrphanMounts([]*state.ArchiveRecord{{
		ArchiveID:  "id1",
		Status:     state.StatusMounted,
		MountPath:  &mp,
		MountPID:   &pid,
	}})
	if len(res.KilledPIDs) != 1 || res.KilledPIDs[0] != 222 {
		t.Fatalf("res=%+v killed=%v", res, killed)
	}
}

func TestBeginMount_returnsExistingLive(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	store, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	archive := filepath.Join(tmp, "live.7z")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	mp := filepath.Join(tmp, "mounts", "live.7z")
	ip := filepath.Join(tmp, "indexes", "live.index.sqlite")
	rec, err = store.Transition(rec.ArchiveID, state.StatusMounting, rec.Status, map[string]any{
		"mount_path": mp,
		"index_path": ip,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	eng := mounter.NewEngine(&config.Config{
		MountRoot:          filepath.Join(tmp, "mounts"),
		IndexDir:           filepath.Join(tmp, "indexes"),
		RatarmountBin:      "true",
		MaxConcurrentMount: 0,
		MaxConcurrentIndex: 1,
	}, store)
	existing := &mounter.ManagedMount{ArchiveID: rec.ArchiveID, PID: 4242}
	eng.Live.Put(existing)

	managed, err := eng.BeginMount(rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if managed != existing {
		t.Fatalf("got %p want %p", managed, existing)
	}
}
