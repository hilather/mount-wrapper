import type { ArchiveRow, SortKey } from './types'

export function filterRows(rows: ArchiveRow[], status: string): ArchiveRow[] {
  if (!status) return rows
  return rows.filter((r) => r.status === status)
}

function numForSort(r: ArchiveRow, key: SortKey): number {
  const m = r.metrics || {}
  const map: Partial<Record<SortKey, number | null | undefined>> = {
    archive_size: m.archive_size_bytes,
    convert_source_size: m.convert_source_size_bytes ?? r.convert_source_size_bytes,
    convert_duration: r.convert_duration_seconds ?? m.convert_duration_seconds,
    index_size: m.index_size_bytes,
    index_duration: r.index_duration_seconds,
    mount_duration: r.mount_duration_seconds,
    extracted_size: m.extracted_size_bytes,
    space_saved: m.space_saved_bytes,
    mount_rss: m.mount_rss_bytes,
  }
  const v = map[key]
  return v === null || v === undefined ? -1 : Number(v)
}

export function sortRows(rows: ArchiveRow[], sortBy: SortKey, desc: boolean): ArchiveRow[] {
  const mul = desc ? -1 : 1
  return [...rows].sort((a, b) => {
    if (sortBy === 'name') {
      return mul * String(a.archive_basename || '').localeCompare(String(b.archive_basename || ''))
    }
    if (sortBy === 'status') {
      return mul * String(a.status || '').localeCompare(String(b.status || ''))
    }
    return mul * (numForSort(a, sortBy) - numForSort(b, sortBy))
  })
}

export const STATUS_FILTER_OPTIONS = [
  '',
  'mounted',
  'converting',
  'indexing',
  'mounting',
  'discovered',
  'hooks_running',
  'index_failed',
  'mount_failed',
  'absent',
  'unmounting',
] as const

export const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: 'name', label: 'Name' },
  { value: 'status', label: 'Status' },
  { value: 'archive_size', label: 'Archive size' },
  { value: 'convert_source_size', label: 'Original size' },
  { value: 'convert_duration', label: 'Convert time' },
  { value: 'index_size', label: 'Index size' },
  { value: 'index_duration', label: 'Index time' },
  { value: 'mount_duration', label: 'Mount time' },
  { value: 'extracted_size', label: 'Extracted size' },
  { value: 'space_saved', label: 'Space saved' },
  { value: 'mount_rss', label: 'Mount RSS' },
]
