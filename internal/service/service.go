package service

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hilather/mount-wrapper/internal/api"
	"github.com/hilather/mount-wrapper/internal/cleaner"
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/hooks"
	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/paths"
	"github.com/hilather/mount-wrapper/internal/reconcile"
	"github.com/hilather/mount-wrapper/internal/scanner"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Options customizes Service construction (tests inject components).
type Options struct {
	Store        *state.Store
	Scanner      *scanner.Scanner
	Engine       *mounter.Engine
	Hooks        *hooks.Runner
	Reconciler   *reconcile.Reconciler
	Cleaner      *cleaner.Cleaner
	Metrics      metrics.MetricsCollector
	AllowAllAuth bool
	Version      string
	// Clock returns Unix seconds (intervals). Nil uses time.Now.
	Clock func() float64
	// SkipPidfile skips exclusive pidfile lock (tests that share paths).
	SkipPidfile bool
	// SkipControl skips binding the Unix control socket (unit tests without a socket path).
	SkipControl bool
	// SkipWeb skips starting the embedded HTTP API even when web_enabled.
	SkipWeb bool
}

// Service orchestrates scanner, engine, hooks, reconciler, and cleaner.
type Service struct {
	Config     *config.Config
	Store      *state.Store
	Scanner    *scanner.Scanner
	Engine     *mounter.Engine
	Hooks      *hooks.Runner
	Reconciler *reconcile.Reconciler
	Cleaner    *cleaner.Cleaner
	// Metrics powers the control "metrics" op (and optional status include_sizes).
	Metrics metrics.MetricsCollector

	AllowAllAuth bool
	Version      string

	Clock func() float64

	// opMu serializes Tick, external HandleRequest (HTTP / direct callers),
	// ConfigSnapshot, ControlActive / InotifyActive, and Shutdown teardown
	// (after HTTP close). Control-plane ServeReady runs only under Tick while
	// opMu is held, so the control Handler is handleRequestLocked (no re-lock)
	// to avoid deadlock. s.control and s.inotify are nil'd under opMu — readers
	// must take opMu (not only s.mu) before following those pointers.
	opMu sync.Mutex

	mu                sync.Mutex
	stop              bool
	reloadRequested   bool
	rescanRequested   bool
	assumeStable      bool
	lastScanAt        float64
	lastReconcileAt   float64
	lastCleanupAt     float64
	lastScanResult    map[string]any
	lastScanAtISO     string
	lastCleanupResult map[string]any
	lowDisk           bool
	started           bool

	// changeNotify is a size-1 broadcast channel for api.ChangeNotifier.
	// NotifyChange non-blocks; slow SSE clients never stall the tick loop.
	changeNotify chan struct{}

	// AfterReload is invoked at the end of each doReload (optional; tests).
	AfterReload func()

	pidfile     *PidFile
	skipPidfile bool
	skipControl bool
	skipWeb     bool
	control     *control.Server
	web         *api.Server
	inotify     *scanner.InotifyWatcher
	ownsStore   bool
}

// New constructs a Service from config, opening the state DB when needed.
func New(cfg *config.Config, opts *Options) (*Service, error) {
	if cfg == nil {
		return nil, serviceErrorf("config is nil")
	}
	if opts == nil {
		opts = &Options{}
	}

	var (
		store *state.Store
		err   error
		owns  bool
	)
	if opts.Store != nil {
		store = opts.Store
	} else {
		store, err = state.Open(cfg.StateDB)
		if err != nil {
			return nil, serviceErrorf("open state db: %v", err)
		}
		owns = true
	}

	sc := opts.Scanner
	if sc == nil {
		sc, err = scanner.New(cfg, store, "", nil, nil)
		if err != nil {
			if owns {
				_ = store.Close()
			}
			return nil, serviceErrorf("scanner: %v", err)
		}
	}

	eng := opts.Engine
	if eng == nil {
		eng = mounter.NewEngine(cfg, store)
	}
	// Best-effort 7z list solid/nested probe when flatten nonsolid is enabled
	// and no probe was injected (tests may set NeedsFlatten explicitly).
	if eng.NeedsFlatten == nil {
		eng.NeedsFlatten = convert.DefaultFlattenNeeded(cfg, eng.ConvertOpts, nil)
	}

	hookRunner := opts.Hooks
	if hookRunner == nil {
		hookRunner = hooks.NewRunner(cfg, store, nil)
	}

	rec := opts.Reconciler
	if rec == nil {
		rec = reconcile.New(cfg, store)
	}
	// Wire engine live registry + ismount/PID probes.
	rec.WithRegistry(eng.Live)
	rec.WithProbes(reconcile.Probes{
		IsMount:  eng.IsMount,
		PIDAlive: mounter.IsProcessAlive,
		Clock:    nil, // default
	})
	// DropLive should also unmount sequence when reconcile kills — engine drop is enough for registry.

	cl := opts.Cleaner
	if cl == nil {
		cl = cleaner.New(cfg, store)
	}
	// Production PathInUse so boot PruneOrphanRatarmountTemps can free unused
	// /tmp/.tmp* without deleting open materializations. Respect injectors that
	// already set PathInUse (tests).
	if cl.PathInUse == nil {
		cl.PathInUse = cleaner.DefaultPathInUse
	}
	cl.Unmounter = cleaner.UnmountFunc(func(archiveID string) error {
		_, err := eng.Unmount(archiveID, false)
		return err
	})
	cl.LivePaths = func() []string {
		var out []string
		for _, m := range eng.Live.Snapshot() {
			if m == nil {
				continue
			}
			// Mount dirs (stale mount cleanup) and archive/cache paths
			// (outer nonsolid age prune must not drop a live source).
			if m.Request.MountPath != "" {
				out = append(out, m.Request.MountPath)
			}
			if m.Request.ArchivePath != "" {
				out = append(out, m.Request.ArchivePath)
			}
		}
		return out
	}
	if eng.IsMount != nil {
		cl.IsMount = cleaner.IsMountFunc(eng.IsMount)
	}

	clock := opts.Clock
	if clock == nil {
		clock = func() float64 {
			return float64(time.Now().UnixNano()) / 1e9
		}
	}

	ver := opts.Version
	if ver == "" {
		ver = "dev"
	}

	mc := opts.Metrics
	if mc == nil {
		c := metrics.NewCollector(&storeMetricsSource{store: store}, metrics.DefaultCollectorConfig())
		// Prefer store convert fields (via ArchiveInput); fill gaps from sidecar
		// (archive_path first, then outer nonsolid cache dest when configured).
		c.Meta = ConvertSidecarMeta{Config: cfg}
		mc = c
	}

	return &Service{
		Config:       cfg,
		Store:        store,
		Scanner:      sc,
		Engine:       eng,
		Hooks:        hookRunner,
		Reconciler:   rec,
		Cleaner:      cl,
		Metrics:      mc,
		AllowAllAuth: opts.AllowAllAuth,
		Version:      ver,
		Clock:        clock,
		changeNotify: make(chan struct{}, 1),
		skipPidfile:  opts.SkipPidfile,
		skipControl:  opts.SkipControl,
		skipWeb:      opts.SkipWeb,
		ownsStore:    owns,
		pidfile:      NewPidFile(cfg.PIDFile),
	}, nil
}

// NotifyChange wakes SSE clients (non-blocking, coalesced). Safe from any
// goroutine; no-op when the service is nil.
func (s *Service) NotifyChange() {
	if s == nil {
		return
	}
	ch := s.changeNotify
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Changes returns the channel used by api.ChangeNotifier (tests / adapters).
// Nil-safe: returns nil when the service has no notify channel.
func (s *Service) Changes() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.changeNotify
}

func (s *Service) now() float64 {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

// Start acquires the pidfile, ensures dirs, partial-index cleanup, boot remount.
func (s *Service) Start() error {
	if s == nil {
		return serviceErrorf("nil service")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// slog level before boot logs; env MOUNT_WRAPPER_LOG_LEVEL overrides config.
	s.applyLogging()

	if !s.skipPidfile {
		if err := s.pidfile.Acquire(); err != nil {
			return err
		}
	}

	// Only create dirs the daemon always needs. Optional converted-output
	// defaults to /var/lib/... in packaged configs — do not mkdir that unless
	// archiveconverter is enabled (parity: create on first convert otherwise).
	dirs := []string{}
	for _, p := range []string{s.Config.MountRoot, s.Config.IndexDir, s.Config.OverlayDir} {
		if p != "" {
			dirs = append(dirs, p)
		}
	}
	if ad := s.Config.ArchivesDir; ad != "" {
		dirs = append(dirs, ad)
	}
	if s.Config.ArchiveconverterEnabled {
		if out := s.Config.ArchiveconverterOutputDir; out != "" {
			dirs = append(dirs, out)
		}
	}
	if err := paths.EnsureServiceDirectories(dirs, nil); err != nil {
		s.releasePidfile()
		return serviceErrorf("ensure service dirs: %v", err)
	}

	if n, err := s.Reconciler.CleanupPartialIndexes(); err != nil {
		slog.Warn("partial index cleanup", "err", err)
	} else if n > 0 {
		slog.Info("partial index cleanup removed files", "count", n)
	}

	if removed, freed := s.Cleaner.PruneOrphanRatarmountTemps(); removed > 0 {
		slog.Info("boot ratarmount temp cleanup", "removed", removed, "freed_bytes", freed)
	}

	if !s.skipControl && s.Config.ControlSocket != "" {
		// handleRequestLocked: ServeReady is only called from Tick while opMu is
		// held. Using HandleRequest here would deadlock on re-lock.
		srv := control.NewServer(s.Config.ControlSocket, s.handleRequestLocked, s.AllowAllAuth)
		srv.GroupName = control.DefaultAuthGroup
		srv.Owner = control.DefaultServiceUser
		srv.OwnerGroup = control.DefaultAuthGroup
		if err := srv.Start(); err != nil {
			s.releasePidfile()
			return serviceErrorf("control socket: %v", err)
		}
		s.control = srv
	}

	s.syncInotify()

	if result, err := s.Reconciler.Boot(); err != nil {
		slog.Warn("boot remount", "err", err)
	} else if len(result.Actions) > 0 {
		slog.Info("boot remount actions", "count", len(result.Actions))
	}

	if removed := s.Cleaner.PruneStaleMountDirs(); len(removed) > 0 {
		slog.Info("boot mount dir cleanup", "removed", len(removed))
	}

	installSignals(s)

	if err := s.startWebIfEnabled(); err != nil {
		if s.control != nil {
			_ = s.control.Close()
			s.control = nil
		}
		s.releasePidfile()
		return err
	}

	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return nil
}

// startWebIfEnabled starts the embedded HTTP API when web_enabled.
func (s *Service) startWebIfEnabled() error {
	if s == nil || s.skipWeb || s.Config == nil || !s.Config.WebEnabled {
		return nil
	}
	host := s.Config.WebHost
	if host == "" {
		host = config.DefaultWebHost
	}
	port := s.Config.WebPort
	if port == 0 {
		port = config.DefaultWebPort
	}
	bind := net.JoinHostPort(host, strconv.Itoa(port))
	srv := api.New(&APIBackend{S: s}, api.ServerOptions{
		Bind:    bind,
		Token:   s.Config.WebToken,
		Version: s.Version,
	})
	// Serve in background; ListenAndServe blocks.
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	// Brief settle: if bind fails, surface quickly.
	select {
	case err := <-errCh:
		if err != nil {
			return serviceErrorf("http api start: %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		// still running — OK
	}
	s.mu.Lock()
	s.web = srv
	s.mu.Unlock()
	return nil
}

// WebAddr returns the bound HTTP address when the web server is running.
func (s *Service) WebAddr() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	web := s.web
	s.mu.Unlock()
	if web == nil {
		return ""
	}
	return web.Bind()
}

// Run starts the service and loops Tick until stop. If once, runs a single tick.
func (s *Service) Run(once bool) error {
	if err := s.Start(); err != nil {
		return err
	}
	defer s.Shutdown()

	if once {
		s.Tick()
		return nil
	}
	for {
		s.mu.Lock()
		stop := s.stop
		s.mu.Unlock()
		if stop {
			break
		}
		s.Tick()
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// Stop requests a graceful exit from the run loop.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stop = true
	s.mu.Unlock()
}

// RequestRescan schedules a scan on the next tick (or immediate when via control).
func (s *Service) RequestRescan(assumeStable bool) {
	s.mu.Lock()
	s.rescanRequested = true
	s.assumeStable = assumeStable
	s.mu.Unlock()
}

// RequestReload schedules a config/source reload.
func (s *Service) RequestReload() {
	s.mu.Lock()
	s.reloadRequested = true
	s.mu.Unlock()
}

// applyLogging sets slog from config log_level, with MOUNT_WRAPPER_LOG_LEVEL
// env override when set and valid.
func (s *Service) applyLogging() {
	if s == nil {
		return
	}
	invalidEnv := config.EnvLogLevelInvalid()
	invalidVal := ""
	if invalidEnv {
		invalidVal = os.Getenv(config.LogLevelEnv)
	}
	applied := config.ApplyEffectiveLogLevel(s.Config)
	if invalidEnv {
		slog.Warn("ignoring invalid "+config.LogLevelEnv, "value", invalidVal)
	}
	slog.Info("log level applied", "level", applied)
}

// syncInotify restarts or stops the Linux inotify watcher from the current
// config and mapped source dirs. Poll remains authoritative; inotify is a hint.
func (s *Service) syncInotify() {
	if s == nil {
		return
	}
	if s.inotify != nil {
		s.inotify.Close()
		s.inotify = nil
	}
	if s.Config == nil || !s.Config.UseInotify {
		return
	}
	s.inotify = scanner.NewInotifyWatcher()
	var dirs []string
	if s.Scanner != nil {
		for _, pair := range s.Scanner.MappedSources() {
			dirs = append(dirs, pair[1])
		}
	}
	watched := s.inotify.Start(dirs)
	if len(watched) > 0 {
		slog.Info("inotify active", "paths", len(watched))
	} else if !s.inotify.Active() {
		// Start closed the watcher when nothing watchable (e.g. all DrvFs).
		s.inotify = nil
	}
}

// InotifyActive reports whether an inotify watcher is currently open (tests).
// Reads s.inotify under opMu so concurrent Shutdown / doReload syncInotify
// cannot race the pointer nil or Close.
func (s *Service) InotifyActive() bool {
	if s == nil {
		return false
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.inotify == nil {
		return false
	}
	return s.inotify.Active()
}

// Shutdown unmounts live work, closes watchers, control socket, releases pidfile, closes store.
//
// Lock order / deadlock avoidance:
//  1. Stop() under s.mu only (flag) — safe while a control "stop" holds opMu.
//  2. Close HTTP without opMu: Server.Close waits for in-flight handlers, and
//     those call HandleRequest/ConfigSnapshot which need opMu. Holding opMu
//     across web.Close would deadlock.
//  3. Take opMu for the rest of teardown so Tick / HandleRequest cannot race
//     Engine/Store/control/inotify cleanup.
//
// Callers must not hold opMu (control "stop" only sets the flag; Run exits the
// loop then defers Shutdown). Idempotent for t.Cleanup double-calls.
func (s *Service) Shutdown() {
	if s == nil {
		return
	}
	slog.Info("service shutting down")
	s.Stop()

	// Drain HTTP first without opMu (see comment above). Swap pointer under
	// s.mu so concurrent WebAddr does not race the nil write.
	s.mu.Lock()
	web := s.web
	s.web = nil
	s.mu.Unlock()
	if web != nil {
		if err := web.Close(); err != nil {
			slog.Warn("http api close", "err", err)
		}
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	if s.control != nil {
		if err := s.control.Close(); err != nil {
			slog.Warn("control socket close", "err", err)
		}
		s.control = nil
	}

	if s.inotify != nil {
		s.inotify.Close()
		s.inotify = nil
	}

	if s.Store != nil {
		if recs, err := s.Store.ListArchives(nil); err == nil {
			for _, rec := range recs {
				switch rec.Status {
				case state.StatusMounted, state.StatusHooksRunning, state.StatusIndexing, state.StatusMounting:
					if _, err := s.Engine.Unmount(rec.ArchiveID, false); err != nil {
						slog.Warn("shutdown unmount", "archive_id", rec.ArchiveID, "err", err)
					}
				}
			}
		}
	}

	s.releasePidfile()

	if s.ownsStore && s.Store != nil {
		_ = s.Store.Close()
		s.Store = nil
	}
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
	slog.Info("service stopped")
}

// ConfigSnapshot returns a deep copy of the effective config under opMu so
// concurrent doReload / config_set cannot race field readers (health, doctor,
// wsl-info). The returned pointer is independent of s.Config.
func (s *Service) ConfigSnapshot() *config.Config {
	if s == nil {
		return nil
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return config.Clone(s.Config)
}

// ControlActive reports whether the Unix control socket is listening.
// Reads s.control under opMu so concurrent Shutdown cannot race the nil write.
func (s *Service) ControlActive() bool {
	if s == nil {
		return false
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.control == nil {
		return false
	}
	return s.control.Active()
}

func (s *Service) releasePidfile() {
	if s.pidfile != nil {
		s.pidfile.Release()
	}
}

// LowDisk reports the last cleaner low-disk flag.
func (s *Service) LowDisk() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lowDisk
}
