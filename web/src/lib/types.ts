/** Shared API / status types for the operator SPA (parity with Go internal/status + metrics). */

export type ConnectionStatus = 'connected' | 'reconnecting' | 'service-down' | 'unknown'

export interface Counts {
  [key: string]: number | undefined
  mounted?: number
  converting?: number
  indexing?: number
  mounting?: number
  discovered?: number
  hooks_running?: number
  index_failed?: number
  mount_failed?: number
  absent?: number
}

export interface ArchiveMetrics {
  archive_id?: string
  archive_path?: string
  archive_basename?: string
  status?: string
  mount_path?: string
  archive_size_bytes?: number | null
  index_size_bytes?: number | null
  /** Primary extracted size for space_saved (deep leaf when known; shallow when opaque nests incomplete). */
  extracted_size_bytes?: number | null
  /** One-level extract: nested archives counted as packed files. */
  extracted_size_shallow_bytes?: number | null
  /** Known deep-leaf content from index (excludes expanded containers + opaque blobs). */
  extracted_size_deep_bytes?: number | null
  space_saved_bytes?: number | null
  space_saved_vs_archive_bytes?: number | null
  convert_source_size_bytes?: number | null
  convert_size_delta_bytes?: number | null
  convert_duration_seconds?: number | null
  index_path?: string
  index_present?: boolean
  extracted_source?: string
  /** deep | shallow | deep_incomplete | mount */
  extracted_nesting?: string
  extracted_deep_complete?: boolean | null
  opaque_nested_count?: number
  opaque_nested_bytes?: number
  error?: string
}

export interface ArchiveRow {
  archive_id: string
  archive_path: string
  archive_basename: string
  source_dir?: string
  status: string
  hooks_status?: string
  mount_path?: string | null
  index_path?: string | null
  overlay_path?: string | null
  mount_pid?: number | null
  mount_attempts?: number
  mount_retryable?: boolean
  fingerprint?: string
  size_bytes?: number
  last_error?: string | null
  last_seen_at?: string
  removed_at?: string | null
  first_mounted_at?: string | null
  hooks_completed_at?: string | null
  index_started_at?: string | null
  index_duration_seconds?: number | null
  mount_duration_seconds?: number | null
  convert_source_size_bytes?: number | null
  convert_duration_seconds?: number | null
  source_fs?: string
  pid_alive?: boolean
  elapsed_s?: number | null
  progress_label?: string
  live_pid?: number | null
  is_first_index?: boolean | null
  mount_phase?: string
  is_mounted?: boolean | null
  /** Nested automount skips from ratarmount-rs (count > 0 when present). */
  nested_skips_count?: number | null
  nested_skips_summary?: string | null
  metrics?: ArchiveMetrics | null
}

export interface MetricsSummary {
  archive_count?: number
  archives_with_extracted_size?: number
  archives_with_convert_metadata?: number
  total_archive_size_bytes?: number
  total_index_size_bytes?: number
  total_extracted_size_bytes?: number
  total_space_saved_bytes?: number
  total_convert_source_size_bytes?: number | null
  total_convert_size_delta_bytes?: number | null
  archives_with_convert_duration?: number | null
  max_convert_duration_seconds?: number | null
}

export interface ArchivesPayload {
  archives?: ArchiveRow[]
  summary?: MetricsSummary | null
  counts?: Counts
  mounted?: number
  indexing?: number
  mounting?: number
  discovered?: number
  hooks_running?: number
  index_failed?: number
  mount_failed?: number
  absent?: number
  converting?: number
  version?: string
  pid?: number
  low_disk?: boolean
  last_scan_at?: string
  indexing_archives?: unknown[]
}

export interface StatusSnapshot extends ArchivesPayload {
  ok?: boolean
  error?: unknown
  generated_at?: string
  metrics_summary?: MetricsSummary | null
}

/** SSE event: `archive` — fine-grained row patch (server ArchiveEventPayload). */
export interface ArchiveSSEEvent {
  archives?: ArchiveRow[]
  removed_ids?: string[]
}

/** SSE event: `scan` — last_scan_at moved. */
export interface ScanSSEEvent {
  last_scan_at?: string
  last_scan?: unknown
  last_scan_duration_ms?: number
}

/** SSE event: `low_disk` — boolean edge (+ optional free/min bytes). */
export interface LowDiskSSEEvent {
  low_disk?: boolean
  disk_free_bytes?: number
  min_free_bytes?: number
}

/** SSE event: `metrics` — metrics_summary when include_sizes path changes. */
export interface MetricsSSEEvent {
  metrics_summary?: MetricsSummary | null
}

export interface HealthPayload {
  ok?: boolean
  web_version?: string
  service_reachable?: boolean
  control_socket?: string
  bind?: string
  service_status_code?: number
  service_error?: unknown
  service_pid?: number
  service_version?: string
  pid?: number
  version?: string
}

export interface WSLInfo {
  distro_name?: string | null
  mount_root?: string
  unc_mounts?: string | null
  hint?: string
}

/** Doctor check severity — matches internal/doctor Severity* and OpenAPI enum. */
export type DoctorSeverity = 'info' | 'warn' | 'error'

/**
 * One diagnostic row from `GET /api/doctor` / `doctor.FormatJSON`.
 * Aligned with docs/openapi.yaml `DoctorCheck` and Go `CheckResult` / ToMap
 * (always includes name, ok, severity, message, details — details never null).
 */
export interface DoctorCheck {
  name: string
  ok: boolean
  severity: DoctorSeverity
  message: string
  details: Record<string, unknown>
}

/**
 * Aggregate doctor payload. Root keys match `Report.ToMap`: ok, checks,
 * config_path (string|null), notes, fixes_applied (arrays, never null).
 * Aligned with docs/openapi.yaml `DoctorReport`.
 */
export interface DoctorReport {
  ok: boolean
  checks: DoctorCheck[]
  config_path?: string | null
  notes: string[]
  fixes_applied: string[]
}

export interface ConfigGetResponse {
  config?: Record<string, unknown>
  config_path?: string
  hot_reload_keys?: string[]
  restart_required_keys?: string[]
  values?: Record<string, unknown>
  [key: string]: unknown
}

export interface ConfigSetResponse {
  ok?: boolean
  written?: boolean
  reloaded?: boolean
  changed_keys?: string[]
  hot_reloadable?: string[]
  restart_required?: string[]
  config?: Record<string, unknown>
  error?: string
  [key: string]: unknown
}

export interface RescanResponse {
  seen?: number
  inserted?: number
  stable?: number
  [key: string]: unknown
}

export interface ActionResponse {
  status?: string
  overlay_action?: string
  [key: string]: unknown
}

/** Per-hook row from GET /api/hooks?archive_id= (control hooks_status). */
export interface HookRow {
  hook_name: string
  status: string
  attempts?: number
  last_exit_code?: number | null
  last_error?: string | null
}

/** Response for GET /api/hooks?archive_id=. */
export interface HooksStatusResponse {
  archive_id: string
  hooks_status?: string
  hooks?: HookRow[]
}

/** Response for GET /api/hooks (no archive_id → hooks_list). */
export interface HooksListResponse {
  hooks?: Array<{ name?: string; path?: string }>
}

/** Per-hook result row from POST /api/hooks (control hooks_run). */
export interface HooksRunResultRow {
  hook_name?: string
  status?: string
  attempts?: number
  exit_code?: number | null
  error?: string
  timed_out?: boolean
  duration_ms?: number
}

/** Response for POST /api/hooks (control hooks_run). */
export interface HooksRunResponse {
  archive_id: string
  ran: boolean
  hooks_status?: string
  force?: boolean
  skipped_reason?: string
  results?: HooksRunResultRow[]
}

export type SortKey =
  | 'name'
  | 'status'
  | 'archive_size'
  | 'convert_source_size'
  | 'convert_duration'
  | 'index_size'
  | 'index_duration'
  | 'mount_duration'
  | 'extracted_size'
  | 'space_saved'

export type ToastKind = 'ok' | 'err' | 'warn' | 'info'

export interface Toast {
  id: number
  kind: ToastKind
  message: string
  html?: boolean
}
