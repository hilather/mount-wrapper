//go:build fuse

package mounter_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Real FUSE integration (optional). Not part of default `make test` / `go test ./...`.
//
// Requires:
//   - /dev/fuse present and usable
//   - ratarmount-rs or ratarmount on PATH
//   - fusermount3/fusermount for cleanup (Linux)
//
// Run:
//
//	go test -tags=fuse ./internal/mounter/ -count=1 -run TestRealFUSEMountUnmount -v
//
// Optional: put a local engine first on PATH, e.g.
//
//	PATH="$HOME/projects/ratarmount-rs/target/release:$PATH" go test -tags=fuse ./internal/mounter/ -count=1

func requireFUSEEnv(t *testing.T) (backend, bin string) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("/dev/fuse not available:", err)
	}
	// Only ratarmount-rs is supported.
	candidates := []struct {
		backend string
		name    string
	}{
		{"rust", "ratarmount-rs"},
	}
	for _, c := range candidates {
		p, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		return c.backend, p
	}
	t.Skip("no ratarmount-rs on PATH (install engine or extend PATH)")
	return "", ""
}

func makeTinyTarGz(t *testing.T, dir string) string {
	t.Helper()
	payload := filepath.Join(dir, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "hello.txt"), []byte("hello fuse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "tiny.tar.gz")
	cmd := exec.Command("tar", "-czf", archive, "-C", payload, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar create: %v\n%s", err, out)
	}
	return archive
}

// TestRealFUSEMountUnmount mounts a tiny tar.gz via Engine (real ratarmount),
// waits for ismount / mounted status, then unmounts.
func TestRealFUSEMountUnmount(t *testing.T) {
	backend, bin := requireFUSEEnv(t)

	tmp := t.TempDir()
	archive := makeTinyTarGz(t, tmp)

	store, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := &config.Config{
		MountRoot:                filepath.Join(tmp, "mounts"),
		IndexDir:                 filepath.Join(tmp, "indexes"),
		OverlayDir:               filepath.Join(tmp, "overlays"),
		StateDB:                  filepath.Join(tmp, "state.db"),
		WriteOverlay:             false,
		MaxConcurrentIndex:       1,
		MaxConcurrentMount:       1,
		MaxMountAttempts:         2,
		MountBackend:             backend,
		RatarmountBin:            bin,
		RatarmountIndexWorkers:   1,
		MountReadyTimeoutSeconds: 120,
		UnmountTimeoutSeconds:    30,
		NameRegex:                config.DefaultNameRegex,
		PollIntervalSeconds:      60,
	}
	for _, d := range []string{cfg.MountRoot, cfg.IndexDir, cfg.OverlayDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	st, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Dir(archive),
		ArchivePath:     archive,
		ArchiveBasename: filepath.Base(archive),
		SizeBytes:       st.Size(),
		MtimeNs:         st.ModTime().UnixNano(),
		Fingerprint:     "fuse-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	eng := mounter.NewEngine(cfg, store)
	// Real StartProcess + DefaultIsMount (no fakes).
	t.Cleanup(func() {
		_, _ = eng.Unmount(rec.ArchiveID, false)
	})

	first := true
	managed, err := eng.BeginMount(rec, &first)
	if err != nil {
		t.Fatalf("BeginMount: %v", err)
	}
	if managed == nil {
		t.Fatal("BeginMount returned nil managed (convert/slot?)")
	}

	mountPath := managed.Request.MountPath
	deadline := time.Now().Add(2 * time.Minute)
	var lastStatus string
	for time.Now().Before(deadline) {
		eng.ProgressLive()
		cur, err := store.GetArchive(rec.ArchiveID)
		if err != nil {
			t.Fatal(err)
		}
		lastStatus = cur.Status
		if cur.Status == state.StatusMounted {
			if !mounter.DefaultIsMount(mountPath) {
				// Status may flip slightly before ismount is visible; poll briefly.
				time.Sleep(50 * time.Millisecond)
				if !mounter.DefaultIsMount(mountPath) {
					t.Fatalf("status mounted but ismount false path=%s", mountPath)
				}
			}
			// Smoke-read a file from the mount if present.
			entries, _ := os.ReadDir(mountPath)
			if len(entries) == 0 {
				t.Logf("warning: mount ready but empty dir listing (engine may still be settling)")
			}
			break
		}
		if cur.Status == state.StatusIndexFailed || cur.Status == state.StatusMountFailed {
			errMsg := ""
			if cur.LastError != nil {
				errMsg = *cur.LastError
			}
			t.Fatalf("mount failed status=%s err=%s backend=%s bin=%s", cur.Status, errMsg, backend, bin)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastStatus != state.StatusMounted {
		t.Fatalf("timeout waiting for mounted; last status=%s backend=%s bin=%s", lastStatus, backend, bin)
	}

	if _, err := eng.Unmount(rec.ArchiveID, false); err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	// Give fusermount a moment; then require clear.
	clearDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(clearDeadline) {
		if !mounter.DefaultIsMount(mountPath) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if mounter.DefaultIsMount(mountPath) {
		t.Fatalf("still mounted after Unmount: %s", mountPath)
	}
}
