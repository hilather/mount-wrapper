import { describe, expect, it } from 'vitest'
import { mergeArchiveRows } from './merge'
import { nextBackoffMs } from './sse'
import { filterRows, sortRows } from './table'
import type { ArchiveRow } from './types'

describe('nextBackoffMs', () => {
  it('doubles until max', () => {
    expect(nextBackoffMs(0, 1000, 30_000)).toBe(1000)
    expect(nextBackoffMs(1, 1000, 30_000)).toBe(2000)
    expect(nextBackoffMs(2, 1000, 30_000)).toBe(4000)
    expect(nextBackoffMs(10, 1000, 30_000)).toBe(30_000)
  })
})

describe('filterRows / sortRows', () => {
  const rows: ArchiveRow[] = [
    {
      archive_id: 'a',
      archive_path: '/a',
      archive_basename: 'beta.tar',
      status: 'mounted',
      metrics: { archive_size_bytes: 100, space_saved_bytes: 50 },
    },
    {
      archive_id: 'b',
      archive_path: '/b',
      archive_basename: 'alpha.tar',
      status: 'indexing',
      metrics: { archive_size_bytes: 200, space_saved_bytes: 10 },
    },
    {
      archive_id: 'c',
      archive_path: '/c',
      archive_basename: 'gamma.tar',
      status: 'mounted',
      metrics: { archive_size_bytes: 50, space_saved_bytes: 5 },
    },
  ]

  it('filters by status', () => {
    expect(filterRows(rows, '').length).toBe(3)
    expect(filterRows(rows, 'mounted').map((r) => r.archive_id)).toEqual(['a', 'c'])
    expect(filterRows(rows, 'indexing').map((r) => r.archive_id)).toEqual(['b'])
  })

  it('sorts by name and size', () => {
    const byName = sortRows(rows, 'name', false)
    expect(byName.map((r) => r.archive_basename)).toEqual([
      'alpha.tar',
      'beta.tar',
      'gamma.tar',
    ])
    const bySizeDesc = sortRows(rows, 'archive_size', true)
    expect(bySizeDesc.map((r) => r.archive_id)).toEqual(['b', 'a', 'c'])
  })
})

describe('mergeArchiveRows', () => {
  it('preserves metrics when next row omits them', () => {
    const prev: ArchiveRow[] = [
      {
        archive_id: 'a',
        archive_path: '/a',
        archive_basename: 'a.tar',
        status: 'indexing',
        metrics: { archive_size_bytes: 99, space_saved_bytes: 10 },
      },
    ]
    const next: ArchiveRow[] = [
      {
        archive_id: 'a',
        archive_path: '/a',
        archive_basename: 'a.tar',
        status: 'mounted',
        metrics: null,
        elapsed_s: null,
      },
    ]
    const merged = mergeArchiveRows(prev, next)
    expect(merged[0].status).toBe('mounted')
    expect(merged[0].metrics?.archive_size_bytes).toBe(99)
  })

  it('prefers new metrics when present', () => {
    const prev: ArchiveRow[] = [
      {
        archive_id: 'a',
        archive_path: '/a',
        archive_basename: 'a.tar',
        status: 'mounted',
        metrics: { archive_size_bytes: 1 },
      },
    ]
    const next: ArchiveRow[] = [
      {
        archive_id: 'a',
        archive_path: '/a',
        archive_basename: 'a.tar',
        status: 'mounted',
        metrics: { archive_size_bytes: 2 },
      },
    ]
    expect(mergeArchiveRows(prev, next)[0].metrics?.archive_size_bytes).toBe(2)
  })
})
