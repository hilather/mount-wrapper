package reconcile

import (
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Reconciler runs one liveness/repair pass against a state store.
//
// Construct with New (from *config.Config) or NewWithSettings. Inject Probes
// and Callbacks for tests or production ismount/process helpers.
//
// The service loop (not this package) should call:
//   - Boot() once at serve start
//   - CleanupPartialIndexes() at serve start (after or as part of Boot)
//   - Reconcile() every config.ReconcileIntervalSeconds (and optionally after poll)
type Reconciler struct {
	Store     *state.Store
	Settings  Settings
	Probes    Probes
	Callbacks Callbacks
}

// New builds a Reconciler from full config (uses max_mount_attempts + mount_ready_timeout).
func New(cfg *config.Config, store *state.Store) *Reconciler {
	s := DefaultSettings()
	if cfg != nil {
		s.MountReadyTimeoutSeconds = cfg.MountReadyTimeoutSeconds
		s.MaxMountAttempts = cfg.MaxMountAttempts
	}
	return NewWithSettings(store, s)
}

// NewWithSettings builds a Reconciler with explicit settings (tests).
func NewWithSettings(store *state.Store, settings Settings) *Reconciler {
	return &Reconciler{
		Store:     store,
		Settings:  settings.Normalize(),
		Probes:    Probes{},
		Callbacks: Callbacks{},
	}
}

// WithProbes returns r for chaining (mutates r).
func (r *Reconciler) WithProbes(p Probes) *Reconciler {
	r.Probes = p
	return r
}

// WithCallbacks returns r for chaining (mutates r).
func (r *Reconciler) WithCallbacks(c Callbacks) *Reconciler {
	r.Callbacks = c
	return r
}

// WithRegistry wires DropLive from a mounter.Registry (does not kill processes;
// serve should install a DropLive that also terminates children).
func (r *Reconciler) WithRegistry(reg *mounter.Registry) *Reconciler {
	if reg == nil {
		return r
	}
	prevDrop := r.Callbacks.DropLive
	r.Callbacks.DropLive = func(archiveID string) {
		reg.Drop(archiveID)
		if prevDrop != nil {
			prevDrop(archiveID)
		}
	}
	r.Probes.Live = func(archiveID string) *LiveSnapshot {
		m := reg.Get(archiveID)
		if m == nil {
			return nil
		}
		snap := &LiveSnapshot{
			Phase:        m.Phase,
			MountPath:    m.Request.MountPath,
			IndexPath:    m.Request.IndexPath,
			ArchivePath:  m.Request.ArchivePath,
			MountBackend: m.Request.MountBackend,
		}
		if m.Cmd != nil && m.Cmd.ProcessState != nil {
			// Already waited.
			snap.Exited = true
			code := m.Cmd.ProcessState.ExitCode()
			snap.ExitCode = &code
		} else if m.Cmd != nil && m.Cmd.Process != nil {
			// Non-blocking: if ProcessState set after Wait elsewhere; without
			// poll API on exec.Cmd we only use PID liveness via store + PIDAlive.
			// Serve can inject a richer Live probe.
			_ = m.PID
		}
		return snap
	}
	return r
}

// Reconcile runs one pass over all non-absent archives: decide + apply.
//
// Order matches Python: list_archives, skip absent, decide, append, apply.
func (r *Reconciler) Reconcile() (Result, error) {
	if r == nil || r.Store == nil {
		return Result{}, reconcileErrorf("reconciler: nil store")
	}
	recs, err := r.Store.ListArchives(nil)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, rec := range recs {
		if rec.Status == state.StatusAbsent {
			continue
		}
		action := DecideOne(rec, r.Settings, r.Probes)
		if action == nil {
			continue
		}
		applied := Apply(r.Store, rec, *action, r.Settings, r.Probes, r.Callbacks)
		result.Actions = append(result.Actions, applied)
	}
	return result, nil
}

// PlanReconcile is DecideOne for all non-absent archives without applying.
func (r *Reconciler) PlanReconcile() (Result, error) {
	if r == nil || r.Store == nil {
		return Result{}, reconcileErrorf("reconciler: nil store")
	}
	recs, err := r.Store.ListArchives(nil)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, rec := range recs {
		if rec.Status == state.StatusAbsent {
			continue
		}
		if action := DecideOne(rec, r.Settings, r.Probes); action != nil {
			result.Actions = append(result.Actions, *action)
		}
	}
	return result, nil
}

// Boot runs the startup remount plan: clear stale PIDs, requeue in-progress
// work, force remount of mounted/hooks_running rows. Does not re-run hooks
// (hooks_status left intact; remount path goes through mount_failed).
//
// Partial-index cleanup is separate (CleanupPartialIndexes) so serve can log
// counts independently, matching Python service start order.
func (r *Reconciler) Boot() (Result, error) {
	if r == nil || r.Store == nil {
		return Result{}, reconcileErrorf("reconciler: nil store")
	}
	recs, err := r.Store.ListArchives(nil)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, rec := range recs {
		action := PlanBoot(rec, r.Probes)
		if action == nil {
			continue
		}
		// Refresh record in case prior applies changed nothing interdependent;
		// boot actions are independent per row.
		applied := Apply(r.Store, rec, *action, r.Settings, r.Probes, r.Callbacks)
		result.Actions = append(result.Actions, applied)
	}
	return result, nil
}

// PlanBootPass returns boot decisions without applying.
func (r *Reconciler) PlanBootPass() (Result, error) {
	if r == nil || r.Store == nil {
		return Result{}, reconcileErrorf("reconciler: nil store")
	}
	recs, err := r.Store.ListArchives(nil)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, rec := range recs {
		if action := PlanBoot(rec, r.Probes); action != nil {
			result.Actions = append(result.Actions, *action)
		}
	}
	return result, nil
}

// CleanupPartialIndexes deletes incomplete indexes for archives that never
// successfully mounted and clears index_path when deleted.
// Returns the number of index files removed.
func (r *Reconciler) CleanupPartialIndexes() (int, error) {
	if r == nil || r.Store == nil {
		return 0, reconcileErrorf("reconciler: nil store")
	}
	recs, err := r.Store.ListArchives(nil)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range recs {
		indexPath := strPtr(rec.IndexPath)
		if !mounter.ShouldDeletePartialIndex(rec.Status, rec.FirstMountedAt, indexPath) {
			continue
		}
		if !mounter.DeleteIndexFile(indexPath) {
			continue
		}
		n++
		if _, err := r.Store.Transition(rec.ArchiveID, rec.Status, rec.Status, map[string]any{
			"index_path": nil,
		}, ""); err != nil {
			return n, err
		}
	}
	return n, nil
}
