package reconcile

import (
	"fmt"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
)

// ActionKind is one corrective (or no-op) decision from the reconciler.
//
// String values match Python ReconcileAction.action for parity and logging.
type ActionKind string

const (
	// ActionOK — in-progress work still healthy, or mount still healthy.
	ActionOK ActionKind = "ok"
	// ActionFailIndex — indexing process dead or mount_ready timeout.
	ActionFailIndex ActionKind = "fail_index"
	// ActionFailMount — mounting/mounted/hooks/converting failure path.
	ActionFailMount ActionKind = "fail_mount"
	// ActionMarkAbsent — unhealthy mount (or stuck unmount) and archive gone.
	ActionMarkAbsent ActionKind = "mark_absent"
	// ActionCleanupIndex — partial index on disk without successful mount.
	ActionCleanupIndex ActionKind = "cleanup_index"
	// ActionRequeue — boot: drop mid-flight indexing/mounting/converting/unmounting.
	// Target is discovered (never mounted) or mount_failed (had first_mounted_at).
	ActionRequeue ActionKind = "requeue"
	// ActionRequestRemount — boot: previously mounted → mount_failed for remount queue.
	ActionRequestRemount ActionKind = "request_remount"
)

// Action is one per-archive decision (and optional apply outcome).
type Action struct {
	ArchiveID      string
	Kind           ActionKind
	Reason         string
	PreviousStatus string
	// TargetStatus is set for boot requeue/remount and for applied fail/absent.
	TargetStatus string
	// ApplyError is set when Apply fails for this action (non-fatal to the pass).
	ApplyError error
}

// Result is the outcome of one Reconcile or Boot pass.
type Result struct {
	Actions []Action
}

// Failures returns actions whose kind starts with "fail".
func (r Result) Failures() []Action {
	var out []Action
	for _, a := range r.Actions {
		if a.Kind == ActionFailIndex || a.Kind == ActionFailMount {
			out = append(out, a)
		}
	}
	return out
}

// HasKind reports whether any action has the given kind.
func (r Result) HasKind(k ActionKind) bool {
	for _, a := range r.Actions {
		if a.Kind == k {
			return true
		}
	}
	return false
}

// ActionFor returns the first action for archiveID, or nil.
func (r Result) ActionFor(archiveID string) *Action {
	for i := range r.Actions {
		if r.Actions[i].ArchiveID == archiveID {
			return &r.Actions[i]
		}
	}
	return nil
}

// Settings are the config knobs reconcile needs (subset of config.Config).
type Settings struct {
	// MountReadyTimeoutSeconds is config.mount_ready_timeout_seconds (default 86400).
	MountReadyTimeoutSeconds float64
	// MaxMountAttempts is config.max_mount_attempts (default 10).
	MaxMountAttempts int
}

// DefaultSettings matches config defaults used when fields are zero.
func DefaultSettings() Settings {
	return Settings{
		MountReadyTimeoutSeconds: 86400,
		MaxMountAttempts:         10,
	}
}

// Normalize fills zero Settings with defaults.
func (s Settings) Normalize() Settings {
	if s.MountReadyTimeoutSeconds <= 0 {
		s.MountReadyTimeoutSeconds = DefaultSettings().MountReadyTimeoutSeconds
	}
	if s.MaxMountAttempts < 1 {
		s.MaxMountAttempts = DefaultSettings().MaxMountAttempts
	}
	return s
}

// MountReadyTimeout returns the timeout as time.Duration.
func (s Settings) MountReadyTimeout() time.Duration {
	return mounter.MountReadyTimeout(s.MountReadyTimeoutSeconds)
}

// LiveSnapshot is optional supervised-process info from the mounter registry.
// When present, reconcile can detect exit-without-DB-update and index-complete
// transitions (parity with Python mounter.live checks).
type LiveSnapshot struct {
	Phase mounter.Phase
	// Exited is true when the child has exited (poll() != nil).
	Exited bool
	// ExitCode is set when Exited (0 = success).
	ExitCode *int
	// Request fields for index verification helpers.
	MountPath    string
	IndexPath    string
	ArchivePath  string
	MountBackend string
}

// Probes are injectable runtime checks (defaults are OS-backed where safe).
type Probes struct {
	// IsMount reports whether path is a FUSE mountpoint.
	// Default: always false (callers/serve must inject a real ismount).
	IsMount func(path string) bool
	// PIDAlive reports whether a process looks alive (signal 0).
	// Default: mounter.IsProcessAlive.
	PIDAlive func(pid int) bool
	// PathExists reports whether the archive source path is a regular file.
	// Default: os.Stat is regular file.
	PathExists func(path string) bool
	// IndexIsFile reports whether an index path is a regular file (for cleanup).
	// Default: os.Stat is regular file.
	IndexIsFile func(path string) bool
	// Clock returns current Unix time in seconds (fractional OK).
	// Default: time.Now().UnixNano()/1e9.
	Clock func() float64
	// Live looks up a supervised child for archiveID. Nil = no live map.
	Live func(archiveID string) *LiveSnapshot
	// ConvertActive reports an in-flight convert job. Nil = none active.
	ConvertActive func(archiveID string) bool
}

// Callbacks are side effects used when applying failures (optional).
type Callbacks struct {
	// DropLive removes archiveID from the mounter live registry and may kill the process.
	DropLive func(archiveID string)
	// UnmountIfMounted is called when a fail path still shows ismount (lazy unmount).
	UnmountIfMounted func(mountPath string)
}

// Error is a reconcile package error.
type Error struct {
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "reconcile error"
	}
	return e.Message
}

func reconcileErrorf(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}
