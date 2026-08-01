/**
 * Display formatters for archives table / overview (parity with upstream web_static).
 */

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

/** Format byte count as human size, or null when missing/invalid. */
export function formatBytes(n: number | null | undefined): string | null {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return null
  const v = Number(n)
  if (v < 0) return null
  let x = v
  let i = 0
  while (x >= 1024 && i < BYTE_UNITS.length - 1) {
    x /= 1024
    i += 1
  }
  const digits = i === 0 ? 0 : x >= 100 ? 0 : x >= 10 ? 1 : 2
  return `${x.toFixed(digits)} ${BYTE_UNITS[i]}`
}

/** Cell display for bytes (em dash when missing). */
export function formatBytesCell(n: number | null | undefined): string {
  return formatBytes(n) ?? '—'
}

/** Format duration in seconds as compact human string. */
export function formatDuration(seconds: number | null | undefined): string | null {
  if (seconds === null || seconds === undefined || Number.isNaN(Number(seconds))) return null
  const total = Number(seconds)
  if (total < 0) return null
  if (total < 60) return `${Math.round(total)}s`
  if (total < 3600) {
    const m = Math.floor(total / 60)
    const s = Math.round(total % 60)
    return s ? `${m}m ${s}s` : `${m}m`
  }
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  return m ? `${h}h ${m}m` : `${h}h`
}

/** Cell display for duration. */
export function formatDurationCell(seconds: number | null | undefined): string {
  return formatDuration(seconds) ?? '—'
}

export interface StatusLike {
  status?: string | null
  progress_label?: string | null
}

/** Status label with optional progress phase for in-progress rows. */
export function formatStatusLabel(r: StatusLike): string {
  const status = r.status || '—'
  if (
    (r.status === 'indexing' || r.status === 'mounting' || r.status === 'converting') &&
    r.progress_label
  ) {
    return `${r.status} · ${r.progress_label}`
  }
  return status
}

export interface ConvertSizeRow {
  convert_source_size_bytes?: number | null
}

export interface ConvertSizeMetrics {
  convert_source_size_bytes?: number | null
  convert_size_delta_bytes?: number | null
  archive_size_bytes?: number | null
}

/** Original (pre-convert) size with optional delta line. */
export function formatOriginalSize(
  r: ConvertSizeRow,
  metrics: ConvertSizeMetrics = {},
): { base: string; delta: string | null } {
  const original =
    metrics.convert_source_size_bytes ?? r.convert_source_size_bytes ?? null
  if (original === null || original === undefined) {
    return { base: '—', delta: null }
  }
  let delta: number | null =
    metrics.convert_size_delta_bytes ??
    (metrics.archive_size_bytes !== null && metrics.archive_size_bytes !== undefined
      ? Number(metrics.archive_size_bytes) - Number(original)
      : null)
  const base = formatBytes(original) ?? '—'
  if (delta === null || delta === undefined || delta === 0) {
    return { base, delta: null }
  }
  return { base, delta: formatConvertDelta(delta) }
}

/** Signed convert delta bytes for display (uses absolute size + sign). */
export function formatConvertDelta(delta: number | null | undefined): string | null {
  if (delta === null || delta === undefined || Number.isNaN(Number(delta))) return null
  const n = Number(delta)
  if (n === 0) return null
  const text = formatBytes(Math.abs(n))
  if (!text) return null
  return n > 0 ? `+${text}` : `-${text}`
}

/** True when status is an active in-progress lifecycle state. */
export function isInProgressStatus(status: string | null | undefined): boolean {
  return status === 'indexing' || status === 'mounting' || status === 'converting'
}

/** True when status is a failed terminal state. */
export function isFailedStatus(status: string | null | undefined): boolean {
  return status === 'index_failed' || status === 'mount_failed'
}

/** Human-friendly status key for overview pills. */
export function formatCountKey(key: string): string {
  return key.replace(/_/g, ' ')
}
