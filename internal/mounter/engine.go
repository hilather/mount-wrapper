package mounter

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/hilather/mount-wrapper/internal/archives"
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/paths"
	"github.com/hilather/mount-wrapper/internal/scanner"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Child status strings returned by CheckChild.
const (
	ChildRunning        = "running"
	ChildIndexComplete  = "index_complete"
	ChildMounted        = "mounted"
	ChildExited         = "exited"
	ChildUnknown        = "unknown"
)

// StartProcessFunc starts a ratarmount child. Nil uses package StartProcess.
type StartProcessFunc func(req MountRequest, opts CmdOptions, archiveMustExist bool) (*exec.Cmd, error)

// Engine orchestrates mount/index/convert/relocate work against a state store.
//
// The service tick is single-threaded: store updates for convert jobs complete
// only on PollConvert (workers must not write the store).
type Engine struct {
	Config *config.Config
	Store  *state.Store
	Live   *Registry

	// IsMount reports FUSE mountpoints. Nil uses DefaultIsMount.
	IsMount IsMountFunc
	// Clock for durations; nil uses time.Now.
	Clock func() time.Time
	// StartProcess override for tests; nil uses package StartProcess.
	StartProcess StartProcessFunc
	// Convert resolve options (bin lookpath). Zero-value is fine.
	ConvertOpts convert.ResolveOptions
	// NeedsFlatten optional 7z structure probe; nil → no flatten convert.
	NeedsFlatten convert.FlattenNeededFunc
	// Run7z optional 7z process runner for zip repack / flatten / outer cache;
	// nil uses convert.DefaultRun7z (real exec). Tests inject fakes / temp scripts.
	Run7z convert.Run7zFunc
	// List7z optional 7z list runner for outer cache solid/encrypted probes;
	// nil uses convert.DefaultList7z. Tests inject fixed listings.
	List7z convert.List7zFunc

	mu                    sync.Mutex
	convertJobs           map[string]*convertJob
	relocateJobs          map[string]*relocateJob
	pendingSourceRemovals map[string]string // archive_id → original path
	polls                 map[string]*ProcessPoll
}

// NewEngine constructs an Engine with an empty live registry.
func NewEngine(cfg *config.Config, store *state.Store) *Engine {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Engine{
		Config:                cfg,
		Store:                 store,
		Live:                  NewRegistry(),
		IsMount:               DefaultIsMount,
		Clock:                 time.Now,
		convertJobs:           make(map[string]*convertJob),
		relocateJobs:          make(map[string]*relocateJob),
		pendingSourceRemovals: make(map[string]string),
		polls:                 make(map[string]*ProcessPoll),
	}
}

func (e *Engine) now() time.Time {
	if e != nil && e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

func (e *Engine) isMount(path string) bool {
	if e != nil && e.IsMount != nil {
		return e.IsMount(path)
	}
	return DefaultIsMount(path)
}

func (e *Engine) startProcess(req MountRequest, opts CmdOptions, mustExist bool) (*exec.Cmd, error) {
	if e != nil && e.StartProcess != nil {
		return e.StartProcess(req, opts, mustExist)
	}
	return StartProcess(req, opts, mustExist)
}

// ConvertJobCount returns active convert jobs.
func (e *Engine) ConvertJobCount() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.convertJobs)
}

// RelocateJobCount returns active relocate jobs (async path).
func (e *Engine) RelocateJobCount() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.relocateJobs)
}

// HasConvertJob reports whether archiveID has an in-flight convert.
func (e *Engine) HasConvertJob(archiveID string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.convertJobs[archiveID]
	return ok
}

// HasRelocateJob reports whether archiveID has an in-flight relocate.
func (e *Engine) HasRelocateJob(archiveID string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.relocateJobs[archiveID]
	return ok
}

// ActiveIndexCount counts indexing rows that hold a live slot or live PID.
func (e *Engine) ActiveIndexCount() int {
	if e == nil || e.Store == nil {
		return 0
	}
	recs, err := e.Store.ListArchives(state.StatusIndexing)
	if err != nil {
		return 0
	}
	n := 0
	for _, rec := range recs {
		if e.holdsIndexSlot(rec) {
			n++
		}
	}
	return n
}

// ActiveMountCount counts mounting rows that hold a live slot or live PID.
func (e *Engine) ActiveMountCount() int {
	if e == nil || e.Store == nil {
		return 0
	}
	recs, err := e.Store.ListArchives(state.StatusMounting)
	if err != nil {
		return 0
	}
	n := 0
	for _, rec := range recs {
		if e.holdsMountSlot(rec) {
			n++
		}
	}
	return n
}

func (e *Engine) holdsIndexSlot(rec *state.ArchiveRecord) bool {
	if rec == nil || rec.Status != state.StatusIndexing {
		return false
	}
	if e.Live != nil && e.Live.Get(rec.ArchiveID) != nil {
		return true
	}
	if rec.MountPID != nil && IsProcessAlive(int(*rec.MountPID)) {
		return true
	}
	return false
}

func (e *Engine) holdsMountSlot(rec *state.ArchiveRecord) bool {
	if rec == nil || rec.Status != state.StatusMounting {
		return false
	}
	if e.Live != nil && e.Live.Get(rec.ArchiveID) != nil {
		return true
	}
	if rec.MountPID != nil && IsProcessAlive(int(*rec.MountPID)) {
		return true
	}
	return false
}

func (e *Engine) indexLimitReached() bool {
	if e.Config == nil {
		return false
	}
	return LimitReached(e.ActiveIndexCount(), e.Config.MaxConcurrentIndex)
}

func (e *Engine) mountLimitReached() bool {
	if e.Config == nil {
		return false
	}
	return LimitReached(e.ActiveMountCount(), e.Config.MaxConcurrentMount)
}

func (e *Engine) convertLimitReached() bool {
	if e.Config == nil {
		return false
	}
	return LimitReached(e.ConvertJobCount(), e.Config.MaxConcurrentConvert)
}

// TakenMountNames returns basenames under mount_root plus stored mount_path names.
func (e *Engine) TakenMountNames() map[string]struct{} {
	names := make(map[string]struct{})
	if e == nil || e.Config == nil {
		return names
	}
	root := e.Config.MountRoot
	if entries, err := os.ReadDir(root); err == nil {
		for _, ent := range entries {
			if ent.IsDir() && ent.Name() != "" && ent.Name()[0] != '.' {
				names[ent.Name()] = struct{}{}
			}
		}
	}
	if e.Store != nil {
		if recs, err := e.Store.ListArchives(nil); err == nil {
			for _, rec := range recs {
				if rec.MountPath != nil && *rec.MountPath != "" {
					names[filepath.Base(*rec.MountPath)] = struct{}{}
				}
			}
		}
	}
	return names
}

func (e *Engine) mountNameForRec(rec *state.ArchiveRecord, taken map[string]struct{}) string {
	var mountName string
	if rec.MountPath != nil && *rec.MountPath != "" {
		mountName = filepath.Base(*rec.MountPath)
		delete(taken, mountName)
	}
	expected := paths.SanitizeMountName(rec.ArchiveBasename, rec.ArchiveID, taken)
	if mountName != "" && mountName != expected {
		return ""
	}
	return mountName
}

func indexPathOf(rec *state.ArchiveRecord) string {
	if rec == nil || rec.IndexPath == nil {
		return ""
	}
	return *rec.IndexPath
}

// BeginMount claims work and starts convert, relocate, or ratarmount.
//
// firstIndex nil is inferred as first_mounted_at == nil.
// Returns (nil, nil) when convert/relocate was queued or a concurrency slot is full.
func (e *Engine) BeginMount(rec *state.ArchiveRecord, firstIndex *bool) (*ManagedMount, error) {
	if e == nil || e.Store == nil || rec == nil {
		return nil, mounterErrorf("begin_mount: engine/store/record not configured")
	}

	// Partial-index cleanup before claim.
	_ = ApplyPartialIndexCleanup(rec.Status, rec.FirstMountedAt, indexPathOf(rec))
	if fresh, err := e.Store.GetArchive(rec.ArchiveID); err == nil && fresh != nil {
		rec = fresh
	}

	fi := firstIndex
	if fi == nil {
		inferred := rec.FirstMountedAt == nil
		fi = &inferred
	}
	needsIndex := ResolveNeedsIndex(
		indexPathOf(rec),
		rec.ArchivePath,
		fi,
		e.Config.ExtraRatarmountArgs,
		e.Config.MountBackend,
		false, // sevenzipAvailable probe not wired; two-phase is safe default
	)

	if rec.Status == state.StatusConverting || e.HasConvertJob(rec.ArchiveID) {
		return nil, nil
	}

	if e.HasRelocateJob(rec.ArchiveID) {
		return nil, nil
	}

	// Reuse a finished conversion product instead of starting a duplicate job.
	if existing := convert.ExistingConvertedPath(e.Config, rec.ArchiveID); existing != "" && rec.ArchivePath != existing {
		updated, err := e.adoptExistingConversion(rec, existing)
		if err != nil {
			slog.Error("adopt existing conversion failed",
				"event", "adopt_existing_conversion_failed",
				"archive_id", rec.ArchiveID,
				"path", existing,
				"err", err,
			)
			return nil, nil
		}
		rec = updated
		if fresh, err := e.Store.GetArchive(rec.ArchiveID); err == nil && fresh != nil {
			rec = fresh
		}
	}

	// Resume mid-flight indexing/mounting without re-queueing convert/relocate.
	if rec.Status == state.StatusIndexing || rec.Status == state.StatusMounting || rec.Status == state.StatusHooksRunning {
		if needsIndex {
			if e.indexLimitReached() {
				return nil, nil
			}
		} else if e.mountLimitReached() {
			return nil, nil
		}
		return e.beginMountProcess(rec, rec.ArchivePath, needsIndex, nil)
	}

	// Pre-convert (archiveconverter / zip repack / flatten).
	if convert.ShouldPreconvert(e.Config, rec.ArchivePath, e.ConvertOpts, e.NeedsFlatten) {
		if e.convertLimitReached() {
			return nil, nil
		}
		if err := e.beginConvertAsync(rec, needsIndex); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Relocate onto Linux FS (sync v1).
	if archives.ShouldRelocate(e.Config, rec) {
		updated, err := e.relocateSync(rec)
		if err != nil {
			return nil, err
		}
		rec = updated
		// Continue into mount/index with updated path.
		needsIndex = ResolveNeedsIndex(
			indexPathOf(rec),
			rec.ArchivePath,
			fi,
			e.Config.ExtraRatarmountArgs,
			e.Config.MountBackend,
			false,
		)
	}

	if needsIndex {
		if e.indexLimitReached() {
			return nil, nil
		}
	} else if e.mountLimitReached() {
		return nil, nil
	}

	return e.beginMountProcess(rec, rec.ArchivePath, needsIndex, nil)
}

func (e *Engine) beginMountProcess(
	rec *state.ArchiveRecord,
	archivePath string,
	needsIndex bool,
	indexOnly *bool,
) (*ManagedMount, error) {
	useIndexOnly := needsIndex
	if indexOnly != nil {
		useIndexOnly = *indexOnly
	}
	if UsesSinglePhaseMount(archivePath, e.Config.ExtraRatarmountArgs, e.Config.MountBackend, false) {
		useIndexOnly = false
		needsIndex = false
	}
	targetStatus := state.StatusMounting
	if useIndexOnly {
		targetStatus = state.StatusIndexing
	}

	// Resolve outer/all nonsolid cache path before building the mount request
	// (parity with resolve_mount_archive_path → ensure_nonsolid_cached_copy).
	// Non-solid sources keep the original path; encrypted / convert failures
	// are recorded after the work-status claim below.
	mountArchive := archivePath
	var outerCacheErr error
	if e.Config != nil && e.Config.Convert7zNonsolid && convert.ScopeUsesOuterCache(e.Config.Convert7zScope) {
		p := convert.NonsolidCacheParamsFromConfig(e.Config, e.ConvertOpts)
		if e.Run7z != nil {
			p.Run7z = e.Run7z
		}
		if e.List7z != nil {
			p.List7z = e.List7z
		}
		resolved, err := convert.EnsureNonsolidCachedCopy(e.Config, archivePath, p)
		if err != nil {
			outerCacheErr = err
			// Keep original path on the request for index/mount path derivation;
			// failStart runs after claim.
		} else {
			mountArchive = resolved
		}
	}

	taken := e.TakenMountNames()
	mountName := e.mountNameForRec(rec, taken)
	req := RequestFromConfig(e.Config, rec.ArchiveID, mountArchive, rec.ArchiveBasename, taken, mountName, "")
	req.IndexOnly = useIndexOnly

	pathFields := map[string]any{
		"index_path": req.IndexPath,
		"mount_path": req.MountPath,
	}
	if req.OverlayPath != "" {
		pathFields["overlay_path"] = req.OverlayPath
	} else {
		pathFields["overlay_path"] = nil
	}
	// Persist outer-cache convert stats on the mount claim when Ensure resolved
	// to a cache dest (store columns for status/SPA durability). Prefer the
	// sidecar next to mountArchive (populate duration or hit size-only backfill);
	// fall back to source Stat for size only. Do not invent duration when the
	// sidecar omits convert_duration_seconds (hit size-only metadata).
	if outerCacheErr == nil && mountArchive != archivePath {
		for k, v := range outerCacheConvertFields(rec, archivePath, mountArchive) {
			pathFields[k] = v
		}
	}

	var err error
	expected := any(rec.Status)
	if rec.Status == targetStatus {
		expected = targetStatus
	}
	// Claim work before path checks / spawn so failure can leave index_failed/mount_failed.
	rec, err = e.Store.Transition(rec.ArchiveID, targetStatus, expected, pathFields, "")
	if err != nil {
		return nil, err
	}

	if outerCacheErr != nil {
		_ = e.failStart(rec, useIndexOnly, outerCacheErr.Error())
		return nil, mounterErrorf("%s", outerCacheErr.Error())
	}

	if err := EnginePathsAllowed(req.IndexPath, req.OverlayPath, req.MountPath, e.Config.AllowIndexesOnDrvfs); err != nil {
		_ = e.failStart(rec, useIndexOnly, err.Error())
		return nil, mounterErrorf("%s", err.Error())
	}

	env := ChildEnvFromConfig(os.Environ(), e.Config.Ratarmount7zDebug, e.Config.RatarmountRustLog)
	env = convert.ApplyNonsolidEnvSlice(env, e.Config)

	// Capture stderr for nested automount skip lines (parity with tarmount-wsl drain).
	var stderrR io.ReadCloser
	var stderrW io.WriteCloser
	if pr, pw, perr := os.Pipe(); perr == nil {
		stderrR, stderrW = pr, pw
	}

	cmdOpts := CmdOptions{Env: env}
	if stderrW != nil {
		cmdOpts.Stderr = stderrW
	}
	cmd, err := e.startProcess(req, cmdOpts, true)
	if stderrW != nil {
		// Parent closes write end so drain sees EOF when the child exits.
		_ = stderrW.Close()
	}
	if err != nil {
		if stderrR != nil {
			_ = stderrR.Close()
		}
		_ = e.failStart(rec, useIndexOnly, err.Error())
		return nil, err
	}

	phase := PhaseMount
	if useIndexOnly {
		phase = PhaseIndexOnly
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	managed := &ManagedMount{
		ArchiveID:    rec.ArchiveID,
		PID:          pid,
		Cmd:          cmd,
		Request:      req,
		StartedAt:    e.now(),
		IsFirstIndex: needsIndex,
		Phase:        phase,
	}
	if stderrR != nil {
		done := make(chan struct{})
		managed.stderrDone = done
		logOther := shouldLogRatarmountStderr(
			e.Config.RatarmountDebug,
			e.Config.Ratarmount7zDebug,
			e.Config.RatarmountLogDir,
			e.Config.RatarmountRustLog,
		)
		go func(id string, r io.ReadCloser, m *ManagedMount, logLines bool) {
			defer close(done)
			defer func() { _ = r.Close() }()
			DrainRatarmountStderr(r, id, func(path, reason string) {
				m.NoteNestedSkip(path)
			}, logLines)
		}(rec.ArchiveID, stderrR, managed, logOther)
	}
	e.Live.Put(managed)
	e.mu.Lock()
	e.polls[rec.ArchiveID] = &ProcessPoll{}
	e.polls[rec.ArchiveID].StartWait(cmd)
	e.mu.Unlock()

	pid64 := int64(pid)
	pathFields["mount_pid"] = pid64
	if _, err := e.Store.Transition(rec.ArchiveID, targetStatus, targetStatus, pathFields, ""); err != nil {
		slog.Warn("update mount_pid failed", "archive_id", rec.ArchiveID, "err", err)
	}
	return managed, nil
}

func (e *Engine) failStart(rec *state.ArchiveRecord, indexPhase bool, reason string) error {
	if rec == nil {
		return mounterErrorf("%s", reason)
	}
	failStatus := state.StatusMountFailed
	if indexPhase {
		failStatus = state.StatusIndexFailed
	}
	attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)
	fields := map[string]any{
		"mount_pid":       nil,
		"mount_attempts":  attempts,
		"mount_retryable": retryable,
		"last_error":      reason,
	}
	expected := []string{rec.Status, state.StatusIndexing, state.StatusMounting, failStatus}
	if _, err := e.Store.Transition(rec.ArchiveID, failStatus, expected, fields, ""); err != nil {
		slog.Warn("failStart transition", "archive_id", rec.ArchiveID, "err", err)
	}
	return mounterErrorf("%s", reason)
}

// outerCacheConvertFields builds optional Transition fields when mount uses an
// outer nonsolid cache dest (mountArchive != sourcePath). Only fills keys where
// rec currently has nil store values. Prefers convert.ReadConvertMetadata on
// the cache path (populate sidecar or Ensure hit size-only backfill); falls
// back to Stat(sourcePath) for convert_source_size_bytes only. Never invents
// convert_duration_seconds when the sidecar omits it (size-only hit metadata).
func outerCacheConvertFields(rec *state.ArchiveRecord, sourcePath, mountArchive string) map[string]any {
	if rec == nil || mountArchive == "" || mountArchive == sourcePath {
		return nil
	}
	if rec.ConvertSourceSizeBytes != nil && rec.ConvertDurationSeconds != nil {
		return nil
	}
	fields := make(map[string]any, 2)
	meta := convert.ReadConvertMetadata(mountArchive)
	if rec.ConvertSourceSizeBytes == nil {
		if meta != nil {
			fields["convert_source_size_bytes"] = meta.OriginalSizeBytes
		} else if st, err := os.Stat(sourcePath); err == nil && st.Mode().IsRegular() {
			fields["convert_source_size_bytes"] = st.Size()
		}
	}
	if rec.ConvertDurationSeconds == nil && meta != nil && meta.ConvertDurationSeconds != nil {
		fields["convert_duration_seconds"] = *meta.ConvertDurationSeconds
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// CheckChild polls one live child.
// Returns running | index_complete | mounted | exited | unknown.
func (e *Engine) CheckChild(archiveID string) string {
	if e == nil || e.Live == nil {
		return ChildUnknown
	}
	managed := e.Live.Get(archiveID)
	if managed == nil {
		return ChildUnknown
	}

	e.mu.Lock()
	poll := e.polls[archiveID]
	if poll == nil {
		poll = &ProcessPoll{}
		e.polls[archiveID] = poll
	}
	e.mu.Unlock()

	exited, exitCode := poll.Poll(managed.Cmd)

	if managed.Phase == PhaseIndexOnly {
		if !exited {
			return ChildRunning
		}
		if IndexBuildVerified(managed.Request.IndexPath, managed.Request.ArchivePath, exitCode, managed.Request.MountBackend) {
			return ChildIndexComplete
		}
		return ChildExited
	}

	if e.isMount(managed.Request.MountPath) {
		if MountIndexRequirementMet(managed.Request.IndexPath, managed.Request.ArchivePath, managed.Request.MountBackend) {
			return ChildMounted
		}
		return ChildRunning
	}
	if !exited {
		return ChildRunning
	}
	return ChildExited
}

// CompleteIndexAndStartMount finishes --no-mount index and starts the FUSE phase.
func (e *Engine) CompleteIndexAndStartMount(archiveID string) (*ManagedMount, error) {
	if e == nil || e.Live == nil {
		return nil, mounterErrorf("complete_index: engine not configured")
	}
	managed := e.Live.Get(archiveID)
	if managed == nil {
		return nil, mounterErrorf("complete_index: no live mount for %s", archiveID)
	}
	if managed.Phase != PhaseIndexOnly {
		return nil, mounterErrorf("complete_index called in phase=%s", managed.Phase)
	}
	rec, err := e.Store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, mounterErrorf("unknown archive_id=%s", archiveID)
	}

	e.mu.Lock()
	poll := e.polls[archiveID]
	e.mu.Unlock()
	var exitCode *int
	if poll != nil {
		_, exitCode = poll.Poll(managed.Cmd)
	}
	if !IndexBuildVerified(managed.Request.IndexPath, managed.Request.ArchivePath, exitCode, managed.Request.MountBackend) {
		return nil, mounterErrorf("index build not verified: path=%s", managed.Request.IndexPath)
	}

	extra := map[string]any{
		"index_path": managed.Request.IndexPath,
	}
	if rec.IndexDurationSeconds == nil {
		elapsed := e.now().Sub(managed.StartedAt).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		extra["index_duration_seconds"] = float64(int(elapsed*1000)) / 1000
	}

	// Finish stderr drain so nested skip summary is complete before index→mount.
	managed.WaitStderrDrain(time.Second)
	skips := managed.NestedSkips()
	LogNestedSkipSummary(archiveID, skips)
	// Persist pure skip advisory into last_error before dropLive so remount /
	// MarkMounted can still surface nested_skips_* when the FUSE-phase child
	// does not re-emit skip lines. Also carried onto the new ManagedMount below.
	if sum := FormatNestedSkipSummary(skips, DefaultNestedSkipSamples); sum != "" {
		extra["last_error"] = sum
	}

	e.dropLive(archiveID)

	rec, err = e.Store.Transition(archiveID, state.StatusMounting, state.StatusIndexing, extra, "")
	if err != nil {
		return nil, err
	}
	falseVal := false
	newManaged, err := e.beginMountProcess(rec, managed.Request.ArchivePath, false, &falseVal)
	if err != nil {
		return nil, err
	}
	// Carry index-phase nested skips onto the FUSE-phase live entry so
	// MarkMounted / status see them even if mount-phase stderr is quiet.
	if newManaged != nil {
		for _, p := range skips {
			newManaged.NoteNestedSkip(p)
		}
	}
	return newManaged, nil
}

// MarkMounted transitions to mounted and drops live bookkeeping.
func (e *Engine) MarkMounted(archiveID string) (*state.ArchiveRecord, error) {
	if e == nil || e.Store == nil {
		return nil, mounterErrorf("mark_mounted: not configured")
	}
	managed := e.Live.Get(archiveID)
	rec, err := e.Store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, mounterErrorf("unknown archive_id=%s", archiveID)
	}

	// last_error cleared on clean mount; when nested automounts were skipped,
	// persist the skip summary so status/SPA can warn operators (no schema migration).
	// If live skips are empty but SQLite already holds a pure nested-skip
	// advisory (index→mount persist, prior mount, remount without re-emit),
	// keep it instead of wiping to nil.
	fields := map[string]any{"last_error": nil}
	if managed != nil {
		// Drain may still be reading automount skips while FUSE is up.
		managed.WaitStderrDrain(time.Second)
		skips := managed.NestedSkips()
		LogNestedSkipSummary(archiveID, skips)
		if sum := FormatNestedSkipSummary(skips, DefaultNestedSkipSamples); sum != "" {
			fields["last_error"] = sum
		} else if rec.LastError != nil && IsNestedSkipOnlyLastError(*rec.LastError) {
			fields["last_error"] = *rec.LastError
		}

		pid := int64(managed.PID)
		fields["mount_pid"] = pid
		if managed.Phase == PhaseMount && rec.Status == state.StatusMounting {
			elapsed := e.now().Sub(managed.StartedAt).Seconds()
			if elapsed < 0 {
				elapsed = 0
			}
			rounded := float64(int(elapsed*1000)) / 1000
			if managed.IsFirstIndex && rec.IndexDurationSeconds == nil &&
				UsesSinglePhaseMount(managed.Request.ArchivePath, managed.Request.ExtraArgs, managed.Request.MountBackend, false) {
				fields["index_duration_seconds"] = rounded
			} else {
				fields["mount_duration_seconds"] = rounded
			}
		}
	} else if rec.MountPID != nil {
		fields["mount_pid"] = *rec.MountPID
		if rec.LastError != nil && IsNestedSkipOnlyLastError(*rec.LastError) {
			fields["last_error"] = *rec.LastError
		}
	} else if rec.LastError != nil && IsNestedSkipOnlyLastError(*rec.LastError) {
		fields["last_error"] = *rec.LastError
	}

	// Keep live entry after mounted so unmount can still signal the FUSE process.
	// Python keeps process alive under mounted status with mount_pid set.
	updated, err := e.Store.Transition(
		archiveID,
		state.StatusMounted,
		[]string{state.StatusIndexing, state.StatusMounting, state.StatusMounted},
		fields,
		"",
	)
	if err != nil {
		return nil, err
	}
	// Live child continues for the FUSE mount; do not drop.
	return updated, nil
}

// MarkFailed records a mount/index failure, drops live, and may delete partial index.
func (e *Engine) MarkFailed(archiveID, reason string) (*state.ArchiveRecord, error) {
	if e == nil || e.Store == nil {
		return nil, mounterErrorf("mark_failed: not configured")
	}
	managed := e.Live.Get(archiveID)
	rec, err := e.Store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, mounterErrorf("unknown archive_id=%s", archiveID)
	}

	indexPhase := managed != nil && managed.Phase == PhaseIndexOnly
	failStatus := state.StatusMountFailed
	if indexPhase {
		failStatus = state.StatusIndexFailed
	}
	attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)

	archivePath := rec.ArchivePath
	extraArgs := e.Config.ExtraRatarmountArgs
	mountBackend := e.Config.MountBackend
	indexPath := indexPathOf(rec)
	if managed != nil {
		archivePath = managed.Request.ArchivePath
		extraArgs = managed.Request.ExtraArgs
		mountBackend = managed.Request.MountBackend
		if managed.Request.IndexPath != "" {
			indexPath = managed.Request.IndexPath
		}
	}

	deleteBadIndex := indexPhase || NeedsFreshIndex(indexPath) ||
		(rec.FirstMountedAt == nil && archivePath != "" &&
			UsesSinglePhaseMount(archivePath, extraArgs, mountBackend, false))
	if deleteBadIndex {
		_ = DeleteIndexFile(indexPath)
	}

	// Include nested automount skips in last_error when present.
	if managed != nil {
		managed.WaitStderrDrain(time.Second)
		skips := managed.NestedSkips()
		LogNestedSkipSummary(archiveID, skips)
		reason = EnrichReasonWithNestedSkips(reason, skips)
	}

	e.dropLive(archiveID)

	fields := map[string]any{
		"mount_pid":       nil,
		"mount_attempts":  attempts,
		"mount_retryable": retryable,
		"last_error":      reason,
	}
	if deleteBadIndex {
		fields["index_path"] = nil
	}
	return e.Store.Transition(
		archiveID,
		failStatus,
		[]string{state.StatusIndexing, state.StatusMounting, failStatus},
		fields,
		"",
	)
}

// ProgressLive advances all live children (index_complete / mounted / exited).
func (e *Engine) ProgressLive() {
	if e == nil || e.Live == nil {
		return
	}
	for archiveID := range e.Live.Snapshot() {
		switch e.CheckChild(archiveID) {
		case ChildIndexComplete:
			if _, err := e.CompleteIndexAndStartMount(archiveID); err != nil {
				slog.Error("index_to_mount failed", "event", "index_to_mount_failed", "archive_id", archiveID, "err", err)
				if _, err2 := e.MarkFailed(archiveID, "index build complete but mount start failed: "+err.Error()); err2 != nil {
					slog.Error("mark_failed", "event", "mark_failed", "archive_id", archiveID, "err", err2)
				}
			} else {
				slog.Info("index build done", "event", "index_build_done", "archive_id", archiveID)
			}
		case ChildMounted:
			if _, err := e.MarkMounted(archiveID); err != nil {
				slog.Error("mark_mounted failed", "event", "mark_mounted_failed", "archive_id", archiveID, "err", err)
			} else {
				slog.Info("mount ready", "event", "mount_ready", "archive_id", archiveID)
			}
		case ChildExited:
			if _, err := e.MarkFailed(archiveID, "ratarmount exited before mount ready"); err != nil {
				slog.Error("mark_failed", "event", "mark_failed", "archive_id", archiveID, "err", err)
			}
		}
	}
}

// Unmount stops the child, clears the mountpoint, and updates state.
// When toAbsent, marks the row absent; otherwise leaves status unmounting.
func (e *Engine) Unmount(archiveID string, toAbsent bool) (*state.ArchiveRecord, error) {
	if e == nil || e.Store == nil {
		return nil, mounterErrorf("unmount: not configured")
	}
	rec, err := e.Store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, mounterErrorf("unknown archive_id=%s", archiveID)
	}

	managed := e.Live.Get(archiveID)
	mountPath := ""
	if managed != nil {
		mountPath = managed.Request.MountPath
	} else if rec.MountPath != nil {
		mountPath = *rec.MountPath
	}

	if rec.Status != state.StatusUnmounting && rec.Status != state.StatusAbsent {
		if updated, err := e.Store.Transition(archiveID, state.StatusUnmounting, rec.Status, map[string]any{
			"mount_pid": nil,
		}, ""); err == nil {
			rec = updated
		} else {
			// Best-effort: continue unmount even if transition fails.
			if fresh, _ := e.Store.GetArchive(archiveID); fresh != nil {
				rec = fresh
			}
		}
	}

	var cmd *exec.Cmd
	var waitPoll *ProcessPoll
	orphanPID := 0
	if managed != nil {
		cmd = managed.Cmd
		e.mu.Lock()
		waitPoll = e.polls[archiveID]
		e.mu.Unlock()
	} else if rec.MountPID != nil {
		orphanPID = int(*rec.MountPID)
	}

	_ = UnmountSequence(cmd, orphanPID, mountPath, UnmountOptions{
		Timeout:  UnmountTimeout(e.Config.UnmountTimeoutSeconds),
		IsMount:  e.isMount,
		WaitPoll: waitPoll,
	})
	e.dropLive(archiveID)

	if toAbsent {
		return e.Store.MarkAbsent(archiveID, "", nil)
	}
	return e.Store.GetArchive(archiveID)
}

func (e *Engine) dropLive(archiveID string) {
	if e.Live != nil {
		e.Live.Drop(archiveID)
	}
	e.mu.Lock()
	delete(e.polls, archiveID)
	e.mu.Unlock()
}

// Fingerprint after convert uses scanner package.
func fingerprintPath(path string, content bool) (string, int64, int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, err
	}
	size := st.Size()
	mtimeNs := st.ModTime().UnixNano()
	fp, err := scanner.ComputeFingerprint(path, size, mtimeNs, content)
	if err != nil {
		return "", 0, 0, err
	}
	return fp, size, mtimeNs, nil
}
