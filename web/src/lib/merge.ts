import type { ArchiveRow } from './types'

/**
 * Merge a single incoming archive row with a previous one so SSE status rows
 * (which omit metrics) do not wipe size columns populated by /api/archives.
 */
export function mergeOneArchiveRow(old: ArchiveRow | undefined, row: ArchiveRow): ArchiveRow {
  if (row.metrics == null && old?.metrics != null) {
    return { ...row, metrics: old.metrics }
  }
  return row
}

/**
 * Merge a full next list with previous rows (snapshot / REST replace path).
 * Rows present only in prev are dropped (full list is authoritative).
 */
export function mergeArchiveRows(prev: ArchiveRow[], next: ArchiveRow[]): ArchiveRow[] {
  const prevById = new Map(prev.map((r) => [r.archive_id, r]))
  return next.map((row) => mergeOneArchiveRow(prevById.get(row.archive_id), row))
}

/**
 * Apply fine-grained SSE `archive` event patches without wiping the table.
 *
 * - `removedIds`: drop rows by archive_id
 * - `patches`: upsert by archive_id, preserving metrics when the patch omits them
 * - Order: removals first, then upserts (new ids append)
 * - No-op when both patches and removedIds are empty (returns `prev`)
 */
export function patchArchiveRows(
  prev: ArchiveRow[],
  patches?: ArchiveRow[] | null,
  removedIds?: string[] | null,
): ArchiveRow[] {
  const remove = new Set((removedIds ?? []).filter((id) => !!id))
  const hasPatches = !!(patches && patches.length > 0)
  if (!hasPatches && remove.size === 0) {
    return prev
  }

  const next = remove.size > 0 ? prev.filter((r) => !remove.has(r.archive_id)) : prev.slice()
  if (!hasPatches) {
    return next
  }

  const indexById = new Map(next.map((r, i) => [r.archive_id, i]))
  for (const patch of patches!) {
    const id = patch?.archive_id
    if (!id) continue
    const idx = indexById.get(id)
    if (idx !== undefined) {
      next[idx] = mergeOneArchiveRow(next[idx], patch)
    } else {
      indexById.set(id, next.length)
      next.push(mergeOneArchiveRow(undefined, patch))
    }
  }
  return next
}
