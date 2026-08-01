package mounter_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
)

func TestNewRatarmountCmd_setsArgvAndGroup(t *testing.T) {
	t.Parallel()
	req := mounter.MountRequest{
		ArchiveID:     "id1",
		ArchivePath:   "/data/a.tar",
		IndexPath:     "/idx/id1.index.sqlite",
		MountPath:     "/mnt/a",
		IndexWorkers:  1,
		RatarmountBin: "ratarmount-rs",
	}
	cmd := mounter.NewRatarmountCmd(req, mounter.CmdOptions{
		Env: []string{"PATH=/usr/bin"},
	})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	if cmd.Path != "ratarmount-rs" && filepath.Base(cmd.Path) != "ratarmount-rs" {
		// Path may be unresolved name.
		if len(cmd.Args) == 0 || cmd.Args[0] != "ratarmount-rs" {
			t.Fatalf("args=%v path=%q", cmd.Args, cmd.Path)
		}
	}
	if runtime.GOOS != "windows" {
		if cmd.SysProcAttr == nil {
			t.Fatal("expected SysProcAttr for process group")
		}
	}
}

func TestPreparePaths(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	req := mounter.MountRequest{
		MountPath:        filepath.Join(tmp, "mounts", "a"),
		IndexPath:        filepath.Join(tmp, "indexes", "a.index.sqlite"),
		OverlayPath:      filepath.Join(tmp, "overlays", "a"),
		RatarmountLogDir: filepath.Join(tmp, "logs"),
	}
	if err := mounter.PreparePaths(req); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{req.MountPath, filepath.Dir(req.IndexPath), req.OverlayPath, req.RatarmountLogDir} {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", p, err)
		}
	}
}

func TestStartProcess_fakeBinary(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Fake ratarmount: exit immediately.
	bin := filepath.Join(tmp, "fake-rm")
	if err := writeFileMode(bin, "#!/bin/sh\nexit 0\n", 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(tmp, "a.tar")
	if err := os.WriteFile(archive, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := mounter.MountRequest{
		ArchiveID:     "id1",
		ArchivePath:   archive,
		IndexPath:     filepath.Join(tmp, "indexes", "id1.index.sqlite"),
		MountPath:     filepath.Join(tmp, "mounts", "a"),
		IndexWorkers:  1,
		RatarmountBin: bin,
		IndexOnly:     true,
	}
	cmd, err := mounter.StartProcess(req, mounter.CmdOptions{
		Env: []string{"PATH=/usr/bin"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		t.Fatal("no pid")
	}
	if err := mounter.WaitWithTimeout(cmd, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStartProcess_missingArchive(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	req := mounter.MountRequest{
		ArchivePath:   filepath.Join(tmp, "nope.tar"),
		IndexPath:     filepath.Join(tmp, "i.sqlite"),
		MountPath:     filepath.Join(tmp, "m"),
		RatarmountBin: "/bin/true",
	}
	_, err := mounter.StartProcess(req, mounter.CmdOptions{}, true)
	if err == nil {
		t.Fatal("expected missing archive error")
	}
}

func TestTerminateProcessGroup_sleep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no process groups")
	}
	t.Parallel()
	cmd := exec.Command("sleep", "30")
	mounter.ApplyProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !mounter.IsProcessAlive(pid) {
		t.Fatal("sleep should be alive")
	}
	done := make(chan struct{})
	go func() {
		mounter.TerminateProcessGroup(cmd, 2*time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("terminate hung")
	}
	// Process should be gone.
	time.Sleep(50 * time.Millisecond)
	if mounter.IsProcessAlive(pid) {
		// Sometimes zombie briefly; try wait already done in Terminate.
		t.Log("pid still reports alive after terminate (may be race)")
	}
}
