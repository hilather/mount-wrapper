package hooks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Runner discovers and runs first-mount hooks for archives.
//
// Store updates are serialized with mu so hooks_parallel process execution can
// still share a single-writer state.Store safely.
type Runner struct {
	Config   *config.Config
	Store    *state.Store
	Security SecurityPolicy

	// Clock returns the current time (tests inject a fake).
	Clock func() time.Time

	// LookPath is used only when needed; empty uses exec.LookPath default via Command.
	// CommandContext builder for tests — if nil, uses default runHookProcess.
	RunProcess func(ctx context.Context, argv []string, env []string, cwd string) (exitCode *int, timedOut bool, stderr string, err error)

	mu sync.Mutex
}

// NewRunner constructs a Runner with DefaultSecurityPolicy when security is nil.
func NewRunner(cfg *config.Config, store *state.Store, security *SecurityPolicy) *Runner {
	pol := DefaultSecurityPolicy()
	if security != nil {
		pol = *security
	}
	return &Runner{
		Config:   cfg,
		Store:    store,
		Security: pol,
		Clock:    time.Now,
	}
}

// ListHooks returns discovered hooks for the configured hooks_dir (no security).
func (r *Runner) ListHooks() []DiscoveredHook {
	if r == nil || r.Config == nil {
		return nil
	}
	return DiscoverHooks(r.Config.HooksDir)
}

// RunForArchive runs the first-mount hook cycle for archiveID.
//
// Transitions mounted → hooks_running → mounted and updates hooks_status /
// per-hook rows. If hooks should not run, returns Ran=false without changing
// mount status.
func (r *Runner) RunForArchive(archiveID string, force bool) (*CycleResult, error) {
	if r == nil || r.Config == nil || r.Store == nil {
		return nil, hookErrorf("runner not configured")
	}

	rec, err := r.Store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, hookErrorf("archive_id=%q not found", archiveID)
	}

	if !force && !ShouldRunHooks(rec.HooksStatus, r.Config.HookRerunOnFailure) {
		return &CycleResult{
			ArchiveID:     archiveID,
			Ran:           false,
			HooksStatus:   rec.HooksStatus,
			SkippedReason: "hooks_status=" + rec.HooksStatus + " is terminal or not eligible",
		}, nil
	}

	hooksDir := r.Config.HooksDir
	if err := ValidateHooksDir(hooksDir, r.Security); err != nil {
		slog.Error("hooks_dir security failed", "err", err)
		rec, err2 := r.enterHooksRunning(rec, nil)
		if err2 != nil {
			return nil, err2
		}
		return r.finishFailed(rec, err.Error(), nil)
	}

	discovered := DiscoverHooks(hooksDir)
	safe := make([]DiscoveredHook, 0, len(discovered))
	for _, h := range discovered {
		real, err := ValidateHookSecurity(h.Path, hooksDir, r.Security)
		if err != nil {
			slog.Error("refusing hook", "hook", h.Name, "err", err)
			// Refuse entire cycle if any discovered hook fails security.
			rec, err2 := r.enterHooksRunning(rec, nil)
			if err2 != nil {
				return nil, err2
			}
			results := []RunResult{{
				HookName: h.Name,
				Status:   state.HookFailed,
				Attempts: 0,
				Error:    err.Error(),
			}}
			return r.finishFailed(rec, "hook security: "+err.Error(), results)
		}
		safe = append(safe, DiscoveredHook{Name: h.Name, Path: real})
	}

	if len(safe) == 0 {
		// No hooks configured — treat as success (nothing to do).
		rec, err = r.enterHooksRunning(rec, nil)
		if err != nil {
			return nil, err
		}
		return r.finishSuccess(rec, nil)
	}

	names := make([]string, len(safe))
	for i, h := range safe {
		names[i] = h.Name
	}
	rec, err = r.enterHooksRunning(rec, names)
	if err != nil {
		return nil, err
	}

	var results []RunResult
	if r.Config.HooksParallel {
		results = r.runParallel(rec, safe)
	} else {
		results = r.runSequential(rec, safe)
	}
	return r.aggregateAndFinish(rec, results)
}

func (r *Runner) runSequential(rec *state.ArchiveRecord, safe []DiscoveredHook) []RunResult {
	results := make([]RunResult, 0, len(safe))
	stop := false
	for _, hook := range safe {
		if stop {
			_ = r.upsertHook(rec.ArchiveID, hook.Name, state.UpsertHookParams{
				Status:    state.HookSkipped,
				LastError: strPtr("skipped_due_to_prior_hard_fail"),
			})
			results = append(results, RunResult{
				HookName: hook.Name,
				Status:   state.HookSkipped,
				Attempts: 0,
				Error:    "skipped_due_to_prior_hard_fail",
			})
			continue
		}

		row := r.getHookRow(rec.ArchiveID, hook.Name)
		if row != nil && row.Status == state.HookSuccess {
			results = append(results, RunResult{
				HookName: hook.Name,
				Status:   state.HookSuccess,
				ExitCode: row.LastExitCode,
				Attempts: row.Attempts,
			})
			continue
		}
		if row != nil && row.Status == state.HookSkipped {
			errMsg := ""
			if row.LastError != nil {
				errMsg = *row.LastError
			}
			results = append(results, RunResult{
				HookName: hook.Name,
				Status:   state.HookSkipped,
				Attempts: row.Attempts,
				Error:    errMsg,
			})
			continue
		}

		result := r.runOne(rec, hook)
		results = append(results, result)
		if result.Status == state.HookFailed && r.Config.HooksStopOnHardFail {
			stop = true
		}
	}
	return results
}

func (r *Runner) runParallel(rec *state.ArchiveRecord, safe []DiscoveredHook) []RunResult {
	// Parallel mode runs eligible hooks concurrently. stop_on_hard_fail cannot
	// skip already-started peers; it only affects sequential mode.
	results := make([]RunResult, len(safe))
	var wg sync.WaitGroup
	for i, hook := range safe {
		row := r.getHookRow(rec.ArchiveID, hook.Name)
		if row != nil && row.Status == state.HookSuccess {
			results[i] = RunResult{
				HookName: hook.Name,
				Status:   state.HookSuccess,
				ExitCode: row.LastExitCode,
				Attempts: row.Attempts,
			}
			continue
		}
		if row != nil && row.Status == state.HookSkipped {
			errMsg := ""
			if row.LastError != nil {
				errMsg = *row.LastError
			}
			results[i] = RunResult{
				HookName: hook.Name,
				Status:   state.HookSkipped,
				Attempts: row.Attempts,
				Error:    errMsg,
			}
			continue
		}
		wg.Add(1)
		go func(idx int, h DiscoveredHook) {
			defer wg.Done()
			results[idx] = r.runOne(rec, h)
		}(i, hook)
	}
	wg.Wait()
	return results
}

func (r *Runner) getHookRow(archiveID, name string) *state.HookRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	hooks, err := r.Store.ListHooks(archiveID)
	if err != nil {
		return nil
	}
	for _, h := range hooks {
		if h.HookName == name {
			return h
		}
	}
	return nil
}

func (r *Runner) upsertHook(archiveID, name string, p state.UpsertHookParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.Store.UpsertHook(archiveID, name, p)
	return err
}

func (r *Runner) enterHooksRunning(rec *state.ArchiveRecord, seedNames []string) (*state.ArchiveRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(seedNames) > 0 {
		if _, err := r.Store.SeedHooks(rec.ArchiveID, seedNames, state.HookPending); err != nil {
			return nil, err
		}
	}

	hooksStatus := rec.HooksStatus
	if hooksStatus == state.HooksNone || hooksStatus == state.HooksPending {
		hooksStatus = state.HooksRunning
	} else if hooksStatus == state.HooksRetry || hooksStatus == state.HooksFailed {
		// resume / re-run → running
		hooksStatus = state.HooksRunning
	} else if hooksStatus != state.HooksRunning {
		hooksStatus = state.HooksRunning
	}

	fields := map[string]any{"hooks_status": hooksStatus}
	if rec.Status == state.StatusHooksRunning {
		return r.Store.Transition(rec.ArchiveID, state.StatusHooksRunning, state.StatusHooksRunning, fields, "")
	}
	return r.Store.Transition(rec.ArchiveID, state.StatusHooksRunning, rec.Status, fields, "")
}

func (r *Runner) runOne(rec *state.ArchiveRecord, hook DiscoveredHook) RunResult {
	row := r.getHookRow(rec.ArchiveID, hook.Name)
	attempts := 1
	if row != nil {
		attempts = row.Attempts + 1
	}
	now := state.UTCNowISO()
	_ = r.upsertHook(rec.ArchiveID, hook.Name, state.UpsertHookParams{
		Status:    state.HookRunning,
		Attempts:  intPtr(attempts),
		LastRunAt: &now,
	})

	archEnv := FromArchiveRecord(rec)
	envMap := BuildHookEnv(archEnv, hook.Name, r.Config.ConfigPath, nil)
	cwd := ResolveHooksCwd(archEnv, r.Config.HooksCwd, r.Config.HooksDir)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		cwd = "/"
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd = "/"
	}

	argv := HookArgv(hook.Path, archEnv)
	timeout := time.Duration(r.Config.HookTimeoutSeconds * float64(time.Second))
	if timeout <= 0 {
		timeout = time.Hour
	}

	t0 := r.now()
	slog.Info("hook start",
		"archive_id", rec.ArchiveID,
		"hook", hook.Name,
		"attempt", attempts,
	)

	var (
		exitCode *int
		timedOut bool
		stderr   string
		spawnErr error
	)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if r.RunProcess != nil {
		exitCode, timedOut, stderr, spawnErr = r.RunProcess(ctx, argv, EnvSlice(envMap), cwd)
	} else {
		exitCode, timedOut, stderr, spawnErr = runHookProcess(ctx, argv, EnvSlice(envMap), cwd)
	}

	duration := r.now().Sub(t0)
	if stderr != "" {
		// Bound log noise
		tail := stderr
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		slog.Debug("hook stderr", "archive_id", rec.ArchiveID, "hook", hook.Name, "stderr", tail)
	}

	if spawnErr != nil && !timedOut && exitCode == nil {
		status := state.HookFailed
		errMsg := "spawn error: " + spawnErr.Error()
		_ = r.upsertHook(rec.ArchiveID, hook.Name, state.UpsertHookParams{
			Status:       status,
			Attempts:     intPtr(attempts),
			LastExitCode: nil,
			LastRunAt:    &now,
			LastError:    &errMsg,
		})
		return RunResult{
			HookName: hook.Name,
			Status:   status,
			Attempts: attempts,
			Error:    errMsg,
			Duration: duration,
		}
	}

	status, classErr := ClassifyExit(exitCode, timedOut, attempts, r.Config.HookMaxRetries)
	errMsg := classErr
	if timedOut && errMsg == "" {
		errMsg = "timeout"
	}

	var lastErr *string
	if errMsg != "" {
		lastErr = &errMsg
	} else {
		empty := ""
		lastErr = &empty
	}
	_ = r.upsertHook(rec.ArchiveID, hook.Name, state.UpsertHookParams{
		Status:       status,
		Attempts:     intPtr(attempts),
		LastExitCode: exitCode,
		LastRunAt:    &now,
		LastError:    lastErr,
	})
	slog.Info("hook done",
		"archive_id", rec.ArchiveID,
		"hook", hook.Name,
		"status", status,
		"exit", exitCode,
		"duration_ms", duration.Milliseconds(),
	)
	return RunResult{
		HookName: hook.Name,
		Status:   status,
		ExitCode: exitCode,
		Attempts: attempts,
		Error:    errMsg,
		TimedOut: timedOut,
		Duration: duration,
	}
}

func runHookProcess(ctx context.Context, argv, env []string, cwd string) (exitCode *int, timedOut bool, stderr string, err error) {
	if len(argv) == 0 {
		return nil, false, "", errors.New("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = cwd
	var stderrBuf bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderrBuf
	// New process group so timeout can kill the whole tree (parity: start_new_session).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, false, stderrBuf.String(), err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case waitErr := <-done:
		stderr = stderrBuf.String()
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			exitCode = &code
		}
		if waitErr != nil && exitCode == nil {
			return nil, false, stderr, waitErr
		}
		return exitCode, false, stderr, nil
	case <-ctx.Done():
		// Timeout: SIGTERM process group, then SIGKILL.
		timedOut = true
		if cmd.Process != nil {
			pgid := cmd.Process.Pid
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				<-done
			}
		} else {
			<-done
		}
		return nil, true, stderrBuf.String(), nil
	}
}

func (r *Runner) aggregateAndFinish(rec *state.ArchiveRecord, results []RunResult) (*CycleResult, error) {
	agg := AggregateStatus(results)
	switch agg {
	case state.HooksFailed:
		return r.finishFailed(rec, "one or more hooks hard-failed", results)
	case state.HooksRetry:
		return r.finishRetry(rec, results)
	default:
		return r.finishSuccess(rec, results)
	}
}

func (r *Runner) finishSuccess(rec *state.ArchiveRecord, results []RunResult) (*CycleResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ts := state.UTCNowISO()
	fields := map[string]any{
		"hooks_status":       state.HooksSuccess,
		"hooks_completed_at": ts,
		"last_error":         nil,
	}
	// Preserve nested-automount skip advisory on last_error (mounted success path).
	// Re-read store so we do not wipe a summary written after this cycle started.
	// Prefer pure skip-only text; if a prior hard-fail/retry enriched
	// "reason; skipped N …", re-store the pure skip segment so SPA still warns.
	if prior := r.priorLastError(rec); prior != "" {
		if mounter.IsNestedSkipOnlyLastError(prior) {
			fields["last_error"] = prior
		} else if sum, n := mounter.ExtractNestedSkipSummary(prior); n > 0 {
			fields["last_error"] = sum
		}
	}
	updated, err := r.Store.Transition(
		rec.ArchiveID,
		state.StatusMounted,
		[]string{state.StatusHooksRunning, state.StatusMounted},
		fields,
		"",
	)
	if err != nil {
		return nil, err
	}
	return &CycleResult{
		ArchiveID:   rec.ArchiveID,
		Ran:         true,
		HooksStatus: updated.HooksStatus,
		Results:     results,
	}, nil
}

func (r *Runner) finishFailed(rec *state.ArchiveRecord, reason string, results []RunResult) (*CycleResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, err := r.Store.GetArchive(rec.ArchiveID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		current = rec
	}
	if current.Status != state.StatusHooksRunning && current.Status != state.StatusMounted {
		if moved, err := r.Store.Transition(
			current.ArchiveID,
			state.StatusHooksRunning,
			current.Status,
			map[string]any{"hooks_status": state.HooksRunning},
			"",
		); err == nil {
			current = moved
		} else {
			// best-effort; try finish from current
			if again, gerr := r.Store.GetArchive(rec.ArchiveID); gerr == nil && again != nil {
				current = again
			}
		}
	}

	// Keep nested-automount skip advisory when overwriting last_error with the
	// hard-fail reason so SPA/status can still extract nested_skips_*.
	prior := ""
	if current.LastError != nil {
		prior = *current.LastError
	} else if rec.LastError != nil {
		prior = *rec.LastError
	}
	lastError := mounter.PreserveNestedSkipInReason(reason, prior)

	ts := state.UTCNowISO()
	updated, err := r.Store.Transition(
		current.ArchiveID,
		state.StatusMounted,
		[]string{state.StatusHooksRunning, state.StatusMounted},
		map[string]any{
			"hooks_status":       state.HooksFailed,
			"hooks_completed_at": ts,
			"last_error":         lastError,
		},
		"",
	)
	if err != nil {
		return nil, err
	}
	return &CycleResult{
		ArchiveID:   rec.ArchiveID,
		Ran:         true,
		HooksStatus: updated.HooksStatus,
		Results:     results,
	}, nil
}

// priorLastError returns the best-effort last_error string for nested-skip
// preservation: store re-read first, then the in-memory rec snapshot.
func (r *Runner) priorLastError(rec *state.ArchiveRecord) string {
	if r != nil && r.Store != nil && rec != nil {
		if cur, err := r.Store.GetArchive(rec.ArchiveID); err == nil && cur != nil && cur.LastError != nil {
			return *cur.LastError
		}
	}
	if rec != nil && rec.LastError != nil {
		return *rec.LastError
	}
	return ""
}

func (r *Runner) finishRetry(rec *state.ArchiveRecord, results []RunResult) (*CycleResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	const retryReason = "one or more hooks returned EX_TEMPFAIL/timeout"
	// Preserve nested-skip advisory (pure or trailing segment) across soft-fail/retry.
	lastError := mounter.PreserveNestedSkipInReason(retryReason, r.priorLastError(rec))

	updated, err := r.Store.Transition(
		rec.ArchiveID,
		state.StatusMounted,
		[]string{state.StatusHooksRunning, state.StatusMounted},
		map[string]any{
			"hooks_status":       state.HooksRetry,
			"hooks_completed_at": nil,
			"last_error":         lastError,
		},
		"",
	)
	if err != nil {
		return nil, err
	}
	return &CycleResult{
		ArchiveID:   rec.ArchiveID,
		Ran:         true,
		HooksStatus: updated.HooksStatus,
		Results:     results,
	}, nil
}

func (r *Runner) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

func intPtr(v int) *int       { return &v }
func strPtr(s string) *string { return &s }
