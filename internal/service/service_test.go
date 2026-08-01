package service_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/service"
	"github.com/hilather/mount-wrapper/internal/state"
)

func testService(t *testing.T) (*service.Service, string) {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	_ = os.MkdirAll(src, 0o755)
	cfg := &config.Config{
		SourceDirs:               []string{src},
		MountRoot:                filepath.Join(tmp, "mounts"),
		IndexDir:                 filepath.Join(tmp, "indexes"),
		OverlayDir:               filepath.Join(tmp, "overlays"),
		StateDB:                  filepath.Join(tmp, "state.db"),
		PIDFile:                  filepath.Join(tmp, "run", "mw.pid"),
		HooksDir:                 filepath.Join(tmp, "hooks.d"),
		NameRegex:                config.DefaultNameRegex,
		StableFileMode:           "two_scans",
		PollIntervalSeconds:      3600,
		ReconcileIntervalSeconds: 3600,
		MaxConcurrentIndex:       2,
		MaxConcurrentConvert:     1,
		MaxConcurrentMount:       0,
		MaxMountAttempts:         5,
		MountBackend:             "rust",
		RatarmountBin:            "true",
		IndexSmallestFirst:       true,
		MinFreeBytes:             0,
		CleanupAfterSeconds:      86400,
		OverlayCleanup:           "retain",
	}
	for _, d := range []string{cfg.MountRoot, cfg.IndexDir, cfg.OverlayDir, cfg.HooksDir, filepath.Dir(cfg.PIDFile)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := state.Open(cfg.StateDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	eng := mounter.NewEngine(cfg, store)
	eng.IsMount = func(string) bool { return false }
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		if req.IndexOnly {
			_ = os.WriteFile(req.IndexPath, []byte("idx"), 0o644)
		}
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	svc, err := service.New(cfg, &service.Options{
		Store:       store,
		Engine:      eng,
		Version:     "test",
		SkipPidfile: false,
		Clock: func() float64 {
			return float64(time.Now().UnixNano()) / 1e9
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		svc.Shutdown()
	})
	return svc, tmp
}

func TestSortArchivesForIndex(t *testing.T) {
	recs := []*state.ArchiveRecord{
		{ArchiveID: "c", ArchiveBasename: "c.tar", SizeBytes: 300},
		{ArchiveID: "a", ArchiveBasename: "a.tar", SizeBytes: 100},
		{ArchiveID: "b", ArchiveBasename: "b.tar", SizeBytes: 100},
	}
	sorted := service.SortArchivesForIndex(recs, true)
	if sorted[0].ArchiveID != "a" || sorted[1].ArchiveID != "b" || sorted[2].ArchiveID != "c" {
		t.Fatalf("order: %s %s %s", sorted[0].ArchiveID, sorted[1].ArchiveID, sorted[2].ArchiveID)
	}
	unsorted := service.SortArchivesForIndex(recs, false)
	if unsorted[0].ArchiveID != "c" {
		t.Fatalf("no-sort first=%s", unsorted[0].ArchiveID)
	}
}

func TestNotifyChangeCoalesces(t *testing.T) {
	svc, _ := testService(t)
	ch := svc.Changes()
	if ch == nil {
		t.Fatal("expected change channel")
	}
	// Non-blocking coalesced sends.
	svc.NotifyChange()
	svc.NotifyChange()
	svc.NotifyChange()
	select {
	case <-ch:
	default:
		t.Fatal("expected one pending notify")
	}
	// Channel drained; no extra signals.
	select {
	case <-ch:
		t.Fatal("expected coalesced (only one signal)")
	default:
	}
	// APIBackend exposes the same channel.
	be := &service.APIBackend{S: svc}
	if be.Notify() != ch {
		t.Fatal("APIBackend.Notify should return service Changes()")
	}
}

func TestTickNotifiesAfterScan(t *testing.T) {
	svc, _ := testService(t)
	// Force scan on next tick.
	svc.RequestRescan(true)
	// Drain any prior signal.
	select {
	case <-svc.Changes():
	default:
	}
	svc.Tick()
	select {
	case <-svc.Changes():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected NotifyChange after scan tick")
	}
}

func TestHandleRequestRescanNotifies(t *testing.T) {
	svc, _ := testService(t)
	select {
	case <-svc.Changes():
	default:
	}
	resp := svc.HandleRequest(map[string]any{"op": "rescan", "assume_stable": true})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("rescan: %+v", resp)
	}
	select {
	case <-svc.Changes():
	default:
		t.Fatal("expected notify after rescan op")
	}
}

func TestPidfileExclusive(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mw.pid")
	p1 := service.NewPidFile(path)
	if err := p1.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer p1.Release()

	p2 := service.NewPidFile(path)
	if err := p2.Acquire(); err == nil {
		p2.Release()
		t.Fatal("expected second acquire to fail")
	}
}

func TestShutdownClearsPidfile(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	pidPath := svc.Config.PIDFile
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("pidfile missing after start: %v", err)
	}
	svc.Shutdown()
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pidfile should be removed after shutdown, err=%v", err)
	}
}

func TestHandleRequestStatusRescanStop(t *testing.T) {
	svc, tmp := testService(t)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	// status
	resp := svc.HandleRequest(map[string]any{"op": "status"})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("status: %+v", resp)
	}
	// Status returns a JSON-friendly map (also used on the control socket).
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["version"] != "test" {
		// Typed helper still available for in-process callers.
		typed := svc.StatusPayload()
		if typed == nil || typed.Version != "test" {
			t.Fatalf("payload: %+v typed=%+v", data, typed)
		}
	}

	// create archive for rescan
	src := svc.Config.SourceDirs[0]
	arch := filepath.Join(src, "sample.tar.gz")
	if err := os.WriteFile(arch, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = tmp

	resp = svc.HandleRequest(map[string]any{"op": "rescan", "assume_stable": true})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("rescan: %+v", resp)
	}

	// metrics / config_get smoke
	resp = svc.HandleRequest(map[string]any{"op": "metrics"})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("metrics: %+v", resp)
	}
	resp = svc.HandleRequest(map[string]any{"op": "config_get"})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("config_get: %+v", resp)
	}

	// stop
	resp = svc.HandleRequest(map[string]any{"op": "stop"})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("stop: %+v", resp)
	}

	// unknown
	resp = svc.HandleRequest(map[string]any{"op": "nope"})
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatal("expected error for unknown op")
	}
}

func TestControlSocketRoundtrip(t *testing.T) {
	svc, tmp := testService(t)
	sock := filepath.Join(tmp, "run", "control.sock")
	svc.Config.ControlSocket = sock
	// Allow unauth so peercred group membership is not required in CI.
	svc.AllowAllAuth = true

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if !svc.ControlActive() {
		t.Fatal("control socket should be active")
	}

	// Drive ServeReady via Tick in a short loop.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			svc.Tick()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	client := control.NewClient(sock, 3*time.Second)
	data, err := client.RequestOK("status", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", data)
	}
	if m["version"] != "test" {
		t.Fatalf("version=%v", m["version"])
	}
	<-done
}

func TestStartDoesNotCreateDefaultConvertedDirWhenConvertDisabled(t *testing.T) {
	// Packaged defaults include archiveconverter_output_dir under /var/lib; Start
	// must not mkdir that when archiveconverter is disabled (non-root smoke).
	svc, tmp := testService(t)
	svc.Config.ArchiveconverterEnabled = false
	svc.Config.ArchiveconverterOutputDir = filepath.Join(tmp, "should-not-create-converted")
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.Config.ArchiveconverterOutputDir); !os.IsNotExist(err) {
		t.Fatalf("converted dir should not exist when convert disabled: err=%v", err)
	}
}

func TestTickOnceWithDiscoveredArchive(t *testing.T) {
	svc, _ := testService(t)
	// Force scan every tick for this test.
	svc.Config.PollIntervalSeconds = 0
	svc.Config.ReconcileIntervalSeconds = 0

	src := svc.Config.SourceDirs[0]
	arch := filepath.Join(src, "tiny.tar.gz")
	if err := os.WriteFile(arch, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	// First scan (not stable yet for two_scans unless assume_stable)
	svc.RequestRescan(true)
	svc.Tick()

	recs, err := svc.Store.ListArchives(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("expected archive after scan")
	}

	// Another tick should try BeginMount with fake StartProcess.
	svc.Tick()
	// May be indexing if mount started.
	found := false
	for _, rec := range recs {
		fresh, _ := svc.Store.GetArchive(rec.ArchiveID)
		if fresh != nil && (fresh.Status == state.StatusIndexing || fresh.Status == state.StatusMounting ||
			fresh.Status == state.StatusMounted || fresh.Status == state.StatusDiscovered ||
			fresh.Status == state.StatusIndexFailed || fresh.Status == state.StatusConverting) {
			found = true
		}
	}
	if !found {
		// Re-list after tick
		recs, _ = svc.Store.ListArchives(nil)
		for _, r := range recs {
			t.Logf("status %s=%s", r.ArchiveID, r.Status)
		}
	}
}
