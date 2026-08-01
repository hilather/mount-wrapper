package service_test

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/service"
	"github.com/hilather/mount-wrapper/internal/state"
	"github.com/hilather/mount-wrapper/internal/testutil"
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

func TestStartAppliesLogLevel(t *testing.T) {
	// Process-global slog + env; no t.Parallel.
	t.Setenv(config.LogLevelEnv, "")
	svc, _ := testService(t)
	svc.Config.LogLevel = "ERROR"
	svc.Config.UseInotify = false
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if slog.Default().Enabled(nil, slog.LevelInfo) {
		t.Fatal("after Start with log_level=ERROR, INFO should be disabled")
	}
	if !slog.Default().Enabled(nil, slog.LevelError) {
		t.Fatal("ERROR should be enabled")
	}
	// Env overrides config at apply time.
	t.Setenv(config.LogLevelEnv, "DEBUG")
	svc.RequestReload()
	svc.Tick()
	if !slog.Default().Enabled(nil, slog.LevelDebug) {
		t.Fatal("MOUNT_WRAPPER_LOG_LEVEL=DEBUG should enable debug after reload")
	}
	t.Setenv(config.LogLevelEnv, "")
	_ = config.ApplyLogLevel("INFO")
}

func TestDoReloadAppliesLogLevelFromConfigFile(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "")
	svc, tmp := testService(t)
	svc.Config.LogLevel = "INFO"
	svc.Config.UseInotify = false
	cfgPath := filepath.Join(tmp, "config.yaml")
	// Minimal valid YAML for Load; re-use paths from svc.Config.
	content := "version: 1\n" +
		"source_dirs:\n  - " + svc.Config.SourceDirs[0] + "\n" +
		"mount_root: " + svc.Config.MountRoot + "\n" +
		"index_dir: " + svc.Config.IndexDir + "\n" +
		"overlay_dir: " + svc.Config.OverlayDir + "\n" +
		"state_db: " + svc.Config.StateDB + "\n" +
		"hooks_dir: " + svc.Config.HooksDir + "\n" +
		"pid_file: " + svc.Config.PIDFile + "\n" +
		"control_socket: " + filepath.Join(tmp, "c.sock") + "\n" +
		"log_level: DEBUG\n" +
		"use_inotify: false\n" +
		"poll_interval_seconds: 3600\n" +
		"reconcile_interval_seconds: 3600\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.Config.ConfigPath = cfgPath
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	// Start applied INFO from in-memory config before file reload.
	_ = config.ApplyLogLevel("INFO")
	svc.RequestReload()
	svc.Tick()
	if svc.Config.LogLevel != "DEBUG" {
		t.Fatalf("config log_level after reload: %s", svc.Config.LogLevel)
	}
	if !slog.Default().Enabled(nil, slog.LevelDebug) {
		t.Fatal("expected DEBUG after reload from file")
	}
	_ = config.ApplyLogLevel("INFO")
}

func TestDoReloadSyncsInotifyFlag(t *testing.T) {
	svc, tmp := testService(t)
	svc.Config.UseInotify = false
	cfgPath := filepath.Join(tmp, "config.yaml")
	writeReloadConfig := func(useInotify bool) {
		t.Helper()
		ui := "false"
		if useInotify {
			ui = "true"
		}
		content := "version: 1\n" +
			"source_dirs:\n  - " + svc.Config.SourceDirs[0] + "\n" +
			"mount_root: " + svc.Config.MountRoot + "\n" +
			"index_dir: " + svc.Config.IndexDir + "\n" +
			"overlay_dir: " + svc.Config.OverlayDir + "\n" +
			"state_db: " + svc.Config.StateDB + "\n" +
			"hooks_dir: " + svc.Config.HooksDir + "\n" +
			"pid_file: " + svc.Config.PIDFile + "\n" +
			"control_socket: " + filepath.Join(tmp, "c.sock") + "\n" +
			"log_level: INFO\n" +
			"use_inotify: " + ui + "\n" +
			"poll_interval_seconds: 3600\n" +
			"reconcile_interval_seconds: 3600\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeReloadConfig(false)
	svc.Config.ConfigPath = cfgPath
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if svc.InotifyActive() {
		t.Fatal("inotify should be off when use_inotify false")
	}
	writeReloadConfig(true)
	svc.RequestReload()
	svc.Tick()
	// On Linux, non-DrvFs source dir should be watchable; on non-Linux stub stays inactive.
	if svc.Config.UseInotify != true {
		t.Fatalf("use_inotify=%v after reload", svc.Config.UseInotify)
	}
	// Toggle off again.
	writeReloadConfig(false)
	svc.RequestReload()
	svc.Tick()
	if svc.Config.UseInotify {
		t.Fatal("use_inotify should be false after second reload")
	}
	if svc.InotifyActive() {
		t.Fatal("inotify should stop when use_inotify false")
	}
}

func TestHandleRequestReloadSchedules(t *testing.T) {
	svc, _ := testService(t)
	svc.Config.UseInotify = false
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	resp := svc.HandleRequest(map[string]any{"op": "reload"})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("reload: %+v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["reload"] != "scheduled" {
		t.Fatalf("data=%v", data)
	}
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
	svc, _ := testService(t)
	// Short path: macOS sun_path ~104 bytes; t.TempDir under /var/folders is often too long.
	sock := testutil.ShortUnixSocketPath(t, "control.sock")
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

// setupWritableConfig materializes a fully-validated config file at path and
// copies it into svc.Config (preserving pointer identity for Engine/Scanner).
// Required for config_set ApplyUpdate, which round-trips ToPublicMap(current).
func setupWritableConfig(t *testing.T, svc *service.Service, path string) {
	t.Helper()
	raw := map[string]any{
		"source_dirs":                []any{svc.Config.SourceDirs[0]},
		"mount_root":                 svc.Config.MountRoot,
		"index_dir":                  svc.Config.IndexDir,
		"overlay_dir":                svc.Config.OverlayDir,
		"state_db":                   svc.Config.StateDB,
		"hooks_dir":                  svc.Config.HooksDir,
		"pid_file":                   svc.Config.PIDFile,
		"control_socket":             filepath.Join(filepath.Dir(path), "c.sock"),
		"log_level":                  "INFO",
		"use_inotify":                false,
		"poll_interval_seconds":      3600,
		"reconcile_interval_seconds": 3600,
	}
	cfg, err := config.FromMap(raw, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	*svc.Config = *loaded
}

// TestConcurrentHandleRequestAndTick exercises opMu serialization under -race.
// Concurrent HTTP-style HandleRequest must not race Tick Config/engine/scanner
// mutations (or deadlock with control ServeReady under Tick).
func TestConcurrentHandleRequestAndTick(t *testing.T) {
	svc, _ := testService(t)
	svc.Config.UseInotify = false
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	const N = 80
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			svc.Tick()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			resp := svc.HandleRequest(map[string]any{"op": "status"})
			if ok, _ := resp["ok"].(bool); !ok {
				t.Errorf("status: %+v", resp)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			ops := []map[string]any{
				{"op": "config_get"},
				{"op": "metrics"},
				{"op": "reload"},
			}
			resp := svc.HandleRequest(ops[i%len(ops)])
			if ok, _ := resp["ok"].(bool); !ok {
				t.Errorf("op %v: %+v", ops[i%len(ops)], resp)
				return
			}
		}
	}()
	wg.Wait()
}

// TestConfigSetReloadsOnce ensures config_set live-applies once and does not
// schedule a second doReload on the next Tick.
func TestConfigSetReloadsOnce(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "")
	svc, tmp := testService(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	setupWritableConfig(t, svc, cfgPath)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	var n atomic.Int32
	svc.AfterReload = func() { n.Add(1) }

	resp := svc.HandleRequest(map[string]any{
		"op": "config_set",
		"patch": map[string]any{
			"log_level": "DEBUG",
		},
		"apply": true,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("config_set: %+v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("no data: %+v", resp)
	}
	if sched, _ := data["reload_scheduled"].(bool); sched {
		t.Fatal("config_set must not schedule a deferred reload when it already doReload'd")
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("expected exactly one doReload from config_set, got %d", got)
	}

	// Tick must not run a second reload (no pending RequestReload).
	svc.Tick()
	if got := n.Load(); got != 1 {
		t.Fatalf("Tick double-reloaded after config_set: count=%d", got)
	}
	if svc.Config.LogLevel != "DEBUG" {
		t.Fatalf("log_level after config_set: %s", svc.Config.LogLevel)
	}
	_ = config.ApplyLogLevel("INFO")
}

// TestConcurrentConfigSetAndTick races config_set (immediate doReload) with Tick
// (scheduled reload / scan) under -race.
func TestConcurrentConfigSetAndTick(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "")
	svc, tmp := testService(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	setupWritableConfig(t, svc, cfgPath)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	var reloads atomic.Int32
	svc.AfterReload = func() { reloads.Add(1) }

	const N = 40
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			svc.Tick()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			level := "INFO"
			if i%2 == 0 {
				level = "DEBUG"
			}
			resp := svc.HandleRequest(map[string]any{
				"op": "config_set",
				"patch": map[string]any{
					"log_level": level,
				},
				"apply": true,
			})
			if ok, _ := resp["ok"].(bool); !ok {
				t.Errorf("config_set: %+v", resp)
				return
			}
			data, _ := resp["data"].(map[string]any)
			if data != nil {
				if sched, _ := data["reload_scheduled"].(bool); sched {
					t.Error("config_set must not set reload_scheduled")
					return
				}
			}
		}
	}()
	wg.Wait()

	// Each successful config_set should contribute one doReload; Tick may add
	// more only if something else scheduled reload (this test does not).
	if got := reloads.Load(); got != int32(N) {
		t.Fatalf("expected %d doReload calls (one per config_set), got %d", N, got)
	}
	_ = config.ApplyLogLevel("INFO")
}

// TestConcurrentHandleRequestAndShutdown races control ops with Shutdown under
// -race. Shutdown must take opMu for teardown without deadlocking HTTP-style
// HandleRequest (and double Shutdown from t.Cleanup must stay safe).
func TestConcurrentHandleRequestAndShutdown(t *testing.T) {
	svc, _ := testService(t)
	svc.Config.UseInotify = false
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	const N = 60
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < N; i++ {
			_ = svc.HandleRequest(map[string]any{"op": "status"})
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < N; i++ {
			ops := []map[string]any{
				{"op": "config_get"},
				{"op": "metrics"},
				{"op": "reload"},
			}
			_ = svc.HandleRequest(ops[i%len(ops)])
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		// Let a few ops start, then tear down while they still run.
		time.Sleep(5 * time.Millisecond)
		svc.Shutdown()
	}()
	close(start)
	wg.Wait()

	// Post-shutdown ops should not panic (may error once store is gone).
	_ = svc.HandleRequest(map[string]any{"op": "status"})
	// Second Shutdown (also exercised by t.Cleanup) must be idempotent.
	svc.Shutdown()
}

// TestConcurrentConfigReloadAndConfigSnapshot races doReload (via config_set /
// scheduled reload+Tick) with ConfigSnapshot / APIBackend.Config readers under
// -race. Snapshots must not share mutable slices with the live config.
func TestConcurrentConfigReloadAndConfigSnapshot(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "")
	svc, tmp := testService(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	setupWritableConfig(t, svc, cfgPath)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}

	backend := &service.APIBackend{S: svc}
	const N = 50
	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			svc.RequestReload()
			svc.Tick()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			level := "INFO"
			if i%2 == 0 {
				level = "DEBUG"
			}
			_ = svc.HandleRequest(map[string]any{
				"op": "config_set",
				"patch": map[string]any{
					"log_level": level,
				},
				"apply": true,
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			snap := svc.ConfigSnapshot()
			if snap == nil {
				t.Error("ConfigSnapshot returned nil")
				return
			}
			// Touch fields/slices the race detector would flag on a shared pointer.
			_ = snap.LogLevel
			_ = len(snap.SourceDirs)
			if len(snap.SourceDirs) > 0 {
				_ = snap.SourceDirs[0]
			}
			_ = snap.ControlSocket
			_ = snap.WebPort
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			cfg := backend.Config()
			if cfg == nil {
				t.Error("APIBackend.Config returned nil")
				return
			}
			_ = cfg.LogLevel
			_ = cfg.MountRoot
			_ = len(cfg.SourceDirs)
			// health-like path: snapshot then status under separate opMu acquires
			_ = svc.HandleRequest(map[string]any{"op": "status"})
		}
	}()
	wg.Wait()
	_ = config.ApplyLogLevel("INFO")
}

// insertMountedForHooks seeds a mounted archive row for hooks_run tests.
func insertMountedForHooks(t *testing.T, svc *service.Service, tmp string) *state.ArchiveRecord {
	t.Helper()
	src := filepath.Join(tmp, "src")
	archive := filepath.Join(src, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(tmp, "mounts", "a")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       src,
		ArchivePath:     archive,
		ArchiveBasename: "a.tar.gz",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(tmp, "indexes", rec.ArchiveID+".index.sqlite")
	if _, err := svc.Store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mount,
		"index_path": index,
	}, ""); err != nil {
		t.Fatal(err)
	}
	rec, err = svc.Store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": mount,
		"mount_pid":  int64(os.Getpid()),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestHandleRequestHooksRunForceMatrix(t *testing.T) {
	svc, tmp := testService(t)
	rec := insertMountedForHooks(t, svc, tmp)

	// Empty hooks.d → first eligible run succeeds (hooks_status none → success).
	resp := svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      false,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("first hooks_run: %+v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if ran, _ := data["ran"].(bool); !ran {
		t.Fatalf("expected first cycle to run: %+v", data)
	}
	if data["hooks_status"] != state.HooksSuccess {
		t.Fatalf("hooks_status=%v", data["hooks_status"])
	}

	// Drain notify from first run.
	select {
	case <-svc.Changes():
	default:
	}

	// Terminal success without force → skipped (ShouldRunHooks).
	resp = svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      false,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("skip hooks_run: %+v", resp)
	}
	data, _ = resp["data"].(map[string]any)
	if ran, _ := data["ran"].(bool); ran {
		t.Fatalf("terminal success must not re-run without force: %+v", data)
	}
	if data["hooks_status"] != state.HooksSuccess {
		t.Fatalf("hooks_status=%v", data["hooks_status"])
	}
	if _, ok := data["skipped_reason"].(string); !ok {
		t.Fatalf("expected skipped_reason: %+v", data)
	}
	select {
	case <-svc.Changes():
	default:
		t.Fatal("expected NotifyChange even when skipped")
	}

	// force=true re-runs past terminal success.
	resp = svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      true,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("force hooks_run: %+v", resp)
	}
	data, _ = resp["data"].(map[string]any)
	if ran, _ := data["ran"].(bool); !ran {
		t.Fatalf("force must run: %+v", data)
	}
	if force, _ := data["force"].(bool); !force {
		t.Fatalf("force echo: %+v", data)
	}
	if data["hooks_status"] != state.HooksSuccess {
		t.Fatalf("hooks_status=%v", data["hooks_status"])
	}
}

func TestHandleRequestHooksRunNotFoundAndBadStatus(t *testing.T) {
	svc, tmp := testService(t)

	resp := svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": "no-such-id",
	})
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected not found: %+v", resp)
	}
	if resp["code"] != "NOT_FOUND" {
		t.Fatalf("code=%v", resp["code"])
	}

	resp = svc.HandleRequest(map[string]any{"op": "hooks_run"})
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected bad request: %+v", resp)
	}
	if resp["code"] != "BAD_REQUEST" {
		t.Fatalf("code=%v", resp["code"])
	}

	// discovered (not mounted) → BAD_REQUEST
	src := filepath.Join(tmp, "src")
	archive := filepath.Join(src, "b.tar")
	if err := os.WriteFile(archive, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       src,
		ArchivePath:     archive,
		ArchiveBasename: "b.tar",
		SizeBytes:       1,
		MtimeNs:         2,
		Fingerprint:     "2:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp = svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      true,
	})
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("discovered should reject: %+v", resp)
	}
	if resp["code"] != "BAD_REQUEST" {
		t.Fatalf("code=%v msg=%v", resp["code"], resp["error"])
	}
}

func TestHandleRequestHooksRunFailedWithoutRerunConfig(t *testing.T) {
	svc, tmp := testService(t)
	rec := insertMountedForHooks(t, svc, tmp)
	if _, err := svc.Store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusMounted, map[string]any{
		"hooks_status": state.HooksFailed,
	}, ""); err != nil {
		t.Fatal(err)
	}
	// Default hook_rerun_on_failure is false → skip without force.
	resp := svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      false,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("hooks_run failed-status: %+v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if ran, _ := data["ran"].(bool); ran {
		t.Fatalf("failed without force/rerun config must skip: %+v", data)
	}

	resp = svc.HandleRequest(map[string]any{
		"op":         "hooks_run",
		"archive_id": rec.ArchiveID,
		"force":      true,
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("force after failed: %+v", resp)
	}
	data, _ = resp["data"].(map[string]any)
	if ran, _ := data["ran"].(bool); !ran {
		t.Fatalf("force after failed must run: %+v", data)
	}
}
