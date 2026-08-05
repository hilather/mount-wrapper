package metrics

// ExtractedSource identifies how extracted (logical) size was obtained.
const (
	ExtractedSourceIndex = "index"
	ExtractedSourceMount = "mount"
)

// Statuses treated as live mounts for promoting mount-walk deep extracted.
const (
	StatusMounted      = "mounted"
	StatusHooksRunning = "hooks_running"
)

// DefaultCacheTTLSeconds is the default TTL for cached per-archive metrics
// (parity with tarmount-wsl DEFAULT_CACHE_TTL_S).
const DefaultCacheTTLSeconds = 60.0

// ArchiveInput is the minimal archive identity needed to compute metrics.
// Paths may be empty when unknown. Convert fields are optional DB/sidecar values.
type ArchiveInput struct {
	ArchiveID              string
	ArchivePath            string
	ArchiveBasename        string
	Status                 string
	MountPath              string
	IndexPath              string
	// MountPID is the live FUSE child PID when mounted (from state.mount_pid).
	// Used to sample process RSS for mount memory metrics.
	MountPID               int
	ConvertSourceSizeBytes *int64
	ConvertDurationSeconds *float64
}

// ArchiveMetrics is size metrics for one archive (parity with Python ArchiveMetrics).
type ArchiveMetrics struct {
	ArchiveID       string `json:"archive_id"`
	ArchivePath     string `json:"archive_path"`
	ArchiveBasename string `json:"archive_basename"`
	Status          string `json:"status"`
	MountPath       string `json:"mount_path,omitempty"`

	ArchiveSizeBytes   *int64 `json:"archive_size_bytes"`
	IndexSizeBytes     *int64 `json:"index_size_bytes"`
	// ExtractedSizeBytes is the primary logical extracted size used for
	// space_saved formulas: deep leaf when known complete (index flatten or
	// mount walk); shallow when opaque nested members remain and no mount deep.
	ExtractedSizeBytes *int64 `json:"extracted_size_bytes"`
	// ExtractedSizeShallowBytes is one-level extract (nested archives as packed files).
	ExtractedSizeShallowBytes *int64 `json:"extracted_size_shallow_bytes,omitempty"`
	// ExtractedSizeDeepBytes is known deep-leaf content from the index (excludes
	// expanded containers and opaque nested blobs). May be a lower bound when
	// ExtractedDeepComplete is false.
	ExtractedSizeDeepBytes *int64 `json:"extracted_size_deep_bytes,omitempty"`

	// SpaceSavedBytes is max(0, extracted − index) when both sizes are known.
	// Primary: bytes avoided by not fully extracting (mount cost ≈ index only).
	// Uses primary ExtractedSizeBytes (deep when known).
	SpaceSavedBytes *int64 `json:"space_saved_bytes"`
	// SpaceSavedVsArchiveBytes is max(0, extracted − archive − index) when all
	// three sizes are known. Secondary: net vs keeping packed archive + extract
	// on the same disk pool (mount footprint = archive + index).
	SpaceSavedVsArchiveBytes *int64 `json:"space_saved_vs_archive_bytes"`

	ConvertSourceSizeBytes *int64   `json:"convert_source_size_bytes,omitempty"`
	ConvertSizeDeltaBytes  *int64   `json:"convert_size_delta_bytes,omitempty"`
	ConvertDurationSeconds *float64 `json:"convert_duration_seconds,omitempty"`

	IndexPath       string `json:"index_path,omitempty"`
	IndexPresent    bool   `json:"index_present"`
	ExtractedSource string `json:"extracted_source,omitempty"` // "index" | "mount" | ""
	// ExtractedNesting is deep | shallow | deep_incomplete | mount (see Nesting* consts).
	ExtractedNesting string `json:"extracted_nesting,omitempty"`
	// ExtractedDeepComplete is true when primary deep covers all nested content
	// (no opaque nested members left unexpanded in the index path).
	ExtractedDeepComplete *bool `json:"extracted_deep_complete,omitempty"`
	// OpaqueNestedCount / OpaqueNestedBytes: nested-looking index members without
	// expanded leaf rows (deep not invented from packed size alone).
	OpaqueNestedCount int   `json:"opaque_nested_count,omitempty"`
	OpaqueNestedBytes int64 `json:"opaque_nested_bytes,omitempty"`

	// MountRSSBytes is the FUSE child process resident set size (RSS) when
	// mount_pid is live. Sampled when metrics are computed (not a durable DB field).
	MountRSSBytes *int64 `json:"mount_rss_bytes,omitempty"`
	// MountRSSPeakBytes is peak RSS (Linux VmHWM) when available.
	MountRSSPeakBytes *int64 `json:"mount_rss_peak_bytes,omitempty"`
	// MountPID echoes the sampled process id (0 omitted).
	MountPID int `json:"mount_pid,omitempty"`

	Error string `json:"error,omitempty"`
}

// Summary aggregates totals across archives (parity with MetricsService.summary).
type Summary struct {
	ArchiveCount                 int      `json:"archive_count"`
	ArchivesWithExtractedSize    int      `json:"archives_with_extracted_size"`
	ArchivesWithConvertMetadata  int      `json:"archives_with_convert_metadata"`
	// ArchivesWithMountRSS is the number of archives with a known mount RSS sample.
	ArchivesWithMountRSS         int      `json:"archives_with_mount_rss"`
	TotalArchiveSizeBytes        int64    `json:"total_archive_size_bytes"`
	TotalIndexSizeBytes          int64    `json:"total_index_size_bytes"`
	TotalExtractedSizeBytes      int64    `json:"total_extracted_size_bytes"`
	TotalSpaceSavedBytes         int64    `json:"total_space_saved_bytes"`
	// TotalMountRSSBytes is the sum of per-archive mount_rss_bytes (live FUSE children).
	TotalMountRSSBytes           int64    `json:"total_mount_rss_bytes"`
	// TotalMountRSSPeakBytes is the sum of peak RSS when known (Linux); 0 if none.
	TotalMountRSSPeakBytes       int64    `json:"total_mount_rss_peak_bytes"`
	TotalConvertSourceSizeBytes  *int64   `json:"total_convert_source_size_bytes"`
	TotalConvertSizeDeltaBytes   *int64   `json:"total_convert_size_delta_bytes"`
	ArchivesWithConvertDuration  *int     `json:"archives_with_convert_duration"`
	MaxConvertDurationSeconds    *float64 `json:"max_convert_duration_seconds"`
}

// ComputeOptions controls how extracted size is resolved for one archive.
type ComputeOptions struct {
	// PreferMount tries the mount walk before the ratarmount index.
	PreferMount bool
	// MountWalk enables FUSE/dir walk fallback when the index is missing or
	// failed. Default true when zero-value is adjusted via WithDefaults.
	// Use a pointer so callers can force false; see NormalizeComputeOptions.
	MountWalk *bool
}

// NormalizeComputeOptions returns opts with MountWalk defaulting to true.
func NormalizeComputeOptions(opts ComputeOptions) ComputeOptions {
	if opts.MountWalk == nil {
		t := true
		opts.MountWalk = &t
	}
	return opts
}

// QueryOptions controls collector cache and extract preference.
type QueryOptions struct {
	// PreferMount is passed through to ComputeOptions.
	PreferMount bool
	// UseCache is true by default; set false for no_cache.
	// When nil, treated as true.
	UseCache *bool
}

// NormalizeQueryOptions returns opts with UseCache defaulting to true.
func NormalizeQueryOptions(opts QueryOptions) QueryOptions {
	if opts.UseCache == nil {
		t := true
		opts.UseCache = &t
	}
	return opts
}

// CollectorConfig is pure configuration for a MetricsCollector (cache TTL, etc.).
type CollectorConfig struct {
	// CacheTTLSeconds is the per-(archive_id, prefer_mount) metrics cache TTL.
	// Zero or negative disables caching (every lookup recomputes). Default is
	// DefaultCacheTTLSeconds when using DefaultCollectorConfig.
	CacheTTLSeconds float64
}

// DefaultCollectorConfig returns CollectorConfig with default TTL.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{CacheTTLSeconds: DefaultCacheTTLSeconds}
}

// BoolPtr returns a *bool for optional flags (tests and call sites).
func BoolPtr(v bool) *bool { return &v }

// Int64Ptr returns a *int64 for optional sizes (tests and call sites).
func Int64Ptr(v int64) *int64 { return &v }

// Float64Ptr returns a *float64 for optional durations (tests and call sites).
func Float64Ptr(v float64) *float64 { return &v }
