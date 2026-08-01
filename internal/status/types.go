package status

import "github.com/hilather/mount-wrapper/internal/metrics"

// LiveMount is a reduced view of a supervised child process for progress labels.
// Phase is mounter.Phase string values: "index_only" | "mount".
type LiveMount struct {
	PID          int
	Phase        string
	IsFirstIndex bool
}

// Options configures Build. Archives are typically from state.Store.ListArchives.
// Clock / PIDAlive / FreeBytes / IsMount are injectable for tests.
type Options struct {
	Version string
	PID     int

	// Config surface used by the status document.
	ConfigPath       string
	OverlayDir       string
	IndexDir         string
	MinFreeBytes     int
	MaxMountAttempts int

	// Archives is the full set of tracked rows (nil/empty OK).
	Archives []*ArchiveInput

	// Live maps archive_id → live supervision state (optional).
	Live map[string]LiveMount

	LastScan    map[string]any
	LastCleanup map[string]any
	LastScanAt  string
	LowDisk     bool

	// Now is Unix seconds; when 0, Clock (or time.Now) is used.
	Now   float64
	Clock func() float64

	// PIDAlive reports whether a mount_pid is alive. Nil → pid>0 only.
	PIDAlive func(pid int) bool

	// FreeBytes probes free space. Nil → omit disk_free_bytes.
	FreeBytes func(path string) (free int64, ok bool)

	// IsMount optionally reports whether mount_path is currently a mount point.
	// When non-nil, each archive dict gets is_mounted (bool).
	IsMount func(path string) bool

	// IncludeSizes merges per-archive metrics + metrics_summary when Metrics is set.
	IncludeSizes bool
	Metrics      MetricsProvider

	// GeneratedAt overrides generated_at (ISO-8601). Empty uses state.UTCNowISO.
	GeneratedAt string
}

// ArchiveInput is the archive fields needed to build status dicts.
// Mirrors state.ArchiveRecord without a hard dependency on store methods.
type ArchiveInput struct {
	ArchiveID              string
	SourceDir              string
	ArchivePath            string
	ArchiveBasename        string
	SizeBytes              int64
	Status                 string
	HooksStatus            string
	MountPath              *string
	IndexPath              *string
	OverlayPath            *string
	MountPID               *int64
	MountAttempts          int
	MountRetryable         bool
	Fingerprint            string
	LastError              *string
	LastSeenAt             string
	RemovedAt              *string
	FirstMountedAt         *string
	HooksCompletedAt       *string
	IndexStartedAt         *string
	IndexDurationSeconds   *float64
	MountDurationSeconds   *float64
	ConvertSourceSizeBytes *int64
	ConvertDurationSeconds *float64
}

// MetricsProvider is the subset of metrics.MetricsCollector used for include_sizes.
type MetricsProvider interface {
	GetAll(opts metrics.QueryOptions, statuses []string) ([]metrics.ArchiveMetrics, error)
	Summary(items []metrics.ArchiveMetrics, opts metrics.QueryOptions) (metrics.Summary, error)
}

// Payload is the full status --json document.
type Payload struct {
	Version    string `json:"version"`
	PID        int    `json:"pid"`
	ConfigPath string `json:"config_path,omitempty"`

	// Top-level convenience counts (subset; full set is Counts).
	Mounted      int `json:"mounted"`
	Indexing     int `json:"indexing"`
	Mounting     int `json:"mounting"`
	Absent       int `json:"absent"`
	MountFailed  int `json:"mount_failed"`
	IndexFailed  int `json:"index_failed"`
	Discovered   int `json:"discovered"`
	HooksRunning int `json:"hooks_running"`

	Counts            map[string]int   `json:"counts"`
	IndexingArchives  []IndexingJob    `json:"indexing_archives"`
	ErrorsRecent      []ErrorEntry     `json:"errors_recent"`
	LastScanAt        string           `json:"last_scan_at,omitempty"`
	LastScanDurationMs *float64        `json:"last_scan_duration_ms,omitempty"`
	LastScan          map[string]any   `json:"last_scan,omitempty"`
	LastCleanup       map[string]any   `json:"last_cleanup,omitempty"`
	DiskFreeBytes     *int64           `json:"disk_free_bytes,omitempty"`
	LowDisk           bool             `json:"low_disk"`
	MinFreeBytes      int              `json:"min_free_bytes"`
	LiveMounts        []string         `json:"live_mounts"`
	Archives          []ArchiveDict    `json:"archives"`
	GeneratedAt       string           `json:"generated_at"`

	// MetricsSummary is set when IncludeSizes merges metrics.
	MetricsSummary *metrics.Summary `json:"metrics_summary,omitempty"`
}

// ArchiveDict is one archive row with optional progress / metrics fields.
type ArchiveDict struct {
	ArchiveID              string   `json:"archive_id"`
	ArchivePath            string   `json:"archive_path"`
	ArchiveBasename        string   `json:"archive_basename"`
	SourceDir              string   `json:"source_dir"`
	Status                 string   `json:"status"`
	HooksStatus            string   `json:"hooks_status"`
	MountPath              *string  `json:"mount_path"`
	IndexPath              *string  `json:"index_path"`
	OverlayPath            *string  `json:"overlay_path"`
	MountPID               *int64   `json:"mount_pid"`
	MountAttempts          int      `json:"mount_attempts"`
	MountRetryable         bool     `json:"mount_retryable"`
	Fingerprint            string   `json:"fingerprint"`
	SizeBytes              int64    `json:"size_bytes"`
	LastError              *string  `json:"last_error"`
	LastSeenAt             string   `json:"last_seen_at"`
	RemovedAt              *string  `json:"removed_at"`
	FirstMountedAt         *string  `json:"first_mounted_at"`
	HooksCompletedAt       *string  `json:"hooks_completed_at"`
	IndexStartedAt         *string  `json:"index_started_at"`
	IndexDurationSeconds   *float64 `json:"index_duration_seconds"`
	MountDurationSeconds   *float64 `json:"mount_duration_seconds"`
	ConvertSourceSizeBytes *int64   `json:"convert_source_size_bytes"`
	ConvertDurationSeconds *float64 `json:"convert_duration_seconds"`
	SourceFS               string   `json:"source_fs"`
	PIDAlive               bool     `json:"pid_alive"`

	// Progress (present when status is converting/indexing/mounting).
	ElapsedS      *float64 `json:"elapsed_s,omitempty"`
	ProgressLabel string   `json:"progress_label,omitempty"`
	LivePID       *int     `json:"live_pid,omitempty"`
	IsFirstIndex  *bool    `json:"is_first_index,omitempty"`
	MountPhase    string   `json:"mount_phase,omitempty"`

	// IsMounted is set only when Options.IsMount is non-nil.
	IsMounted *bool `json:"is_mounted,omitempty"`

	// Metrics is attached when IncludeSizes is true.
	Metrics *metrics.ArchiveMetrics `json:"metrics,omitempty"`
}

// IndexingJob is a compact in-progress index/mount/convert entry.
type IndexingJob struct {
	ArchiveID     string   `json:"archive_id"`
	Path          string   `json:"path"`
	Basename      string   `json:"basename"`
	Status        string   `json:"status"`
	ElapsedS      *float64 `json:"elapsed_s"`
	SourceFS      string   `json:"source_fs"`
	MountPID      *int64   `json:"mount_pid"`
	PIDAlive      bool     `json:"pid_alive"`
	LivePID       *int     `json:"live_pid,omitempty"`
	MountPhase    string   `json:"mount_phase,omitempty"`
	ProgressLabel string   `json:"progress_label,omitempty"`
}

// ErrorEntry is a failed or stuck archive for the status surface.
type ErrorEntry struct {
	ArchiveID      string  `json:"archive_id"`
	Basename       string  `json:"basename"`
	Path           string  `json:"path"`
	Status         string  `json:"status"`
	MountAttempts  int     `json:"mount_attempts"`
	MountRetryable bool    `json:"mount_retryable"`
	LastError      *string `json:"last_error"`
	Stuck          bool    `json:"stuck"`
}
