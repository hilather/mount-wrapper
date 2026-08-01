import type { ArchiveRow } from './types'

/**
 * Merge incoming archive rows with previous ones so SSE status snapshots
 * (which omit metrics) do not wipe size columns populated by /api/archives.
 */
export function mergeArchiveRows(prev: ArchiveRow[], next: ArchiveRow[]): ArchiveRow[] {
  const prevById = new Map(prev.map((r) => [r.archive_id, r]))
  return next.map((row) => {
    const old = prevById.get(row.archive_id)
    if (row.metrics == null && old?.metrics != null) {
      return { ...row, metrics: old.metrics }
    }
    return row
  })
}
