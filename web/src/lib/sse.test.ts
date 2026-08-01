import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  mergeArchiveRows,
  mergeOneArchiveRow,
  patchArchiveRows,
} from './merge'
import { createSSEClient, nextBackoffMs } from './sse'
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

describe('mergeArchiveRows / mergeOneArchiveRow', () => {
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

  it('mergeOneArchiveRow preserves metrics on partial status patch', () => {
    const old: ArchiveRow = {
      archive_id: 'x',
      archive_path: '/x',
      archive_basename: 'x.tar',
      status: 'indexing',
      metrics: { archive_size_bytes: 42, space_saved_bytes: 7 },
    }
    const patch: ArchiveRow = {
      archive_id: 'x',
      archive_path: '/x',
      archive_basename: 'x.tar',
      status: 'mounted',
      progress_label: 'done',
      // metrics omitted (undefined) — same as status SSE rows
    }
    const merged = mergeOneArchiveRow(old, patch)
    expect(merged.status).toBe('mounted')
    expect(merged.progress_label).toBe('done')
    expect(merged.metrics?.archive_size_bytes).toBe(42)
  })
})

describe('patchArchiveRows', () => {
  const base: ArchiveRow[] = [
    {
      archive_id: 'a',
      archive_path: '/a',
      archive_basename: 'a.tar',
      status: 'mounted',
      metrics: { archive_size_bytes: 100 },
    },
    {
      archive_id: 'b',
      archive_path: '/b',
      archive_basename: 'b.tar',
      status: 'indexing',
      metrics: { archive_size_bytes: 200 },
    },
  ]

  it('returns same array on empty patch', () => {
    expect(patchArchiveRows(base, [], [])).toBe(base)
    expect(patchArchiveRows(base, undefined, undefined)).toBe(base)
  })

  it('patches status by archive_id without full wipe', () => {
    const next = patchArchiveRows(base, [
      {
        archive_id: 'a',
        archive_path: '/a',
        archive_basename: 'a.tar',
        status: 'unmounting',
        metrics: null,
      },
    ])
    expect(next).toHaveLength(2)
    expect(next[0].status).toBe('unmounting')
    expect(next[0].metrics?.archive_size_bytes).toBe(100)
    expect(next[1]).toEqual(base[1])
  })

  it('removes by removed_ids', () => {
    const next = patchArchiveRows(base, [], ['b', 'missing'])
    expect(next.map((r) => r.archive_id)).toEqual(['a'])
    expect(next[0].metrics?.archive_size_bytes).toBe(100)
  })

  it('upserts new archive_id and removes another', () => {
    const next = patchArchiveRows(
      base,
      [
        {
          archive_id: 'c',
          archive_path: '/c',
          archive_basename: 'c.tar',
          status: 'discovered',
        },
      ],
      ['a'],
    )
    expect(next.map((r) => r.archive_id)).toEqual(['b', 'c'])
    expect(next[0].status).toBe('indexing')
    expect(next[1].status).toBe('discovered')
  })

  it('does not wipe sibling rows when patching one', () => {
    const next = patchArchiveRows(base, [
      {
        archive_id: 'b',
        archive_path: '/b',
        archive_basename: 'b.tar',
        status: 'mounted',
      },
    ])
    expect(next).toHaveLength(2)
    expect(next.find((r) => r.archive_id === 'a')).toEqual(base[0])
    expect(next.find((r) => r.archive_id === 'b')?.status).toBe('mounted')
    expect(next.find((r) => r.archive_id === 'b')?.metrics?.archive_size_bytes).toBe(200)
  })
})

/** Minimal EventSource mock for createSSEClient unit tests. */
class MockEventSource {
  static instances: MockEventSource[] = []
  url: string
  onopen: ((ev: Event) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  #listeners = new Map<string, Set<(ev: Event) => void>>()

  constructor(url: string) {
    this.url = url
    MockEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: (ev: Event) => void) {
    if (!this.#listeners.has(type)) this.#listeners.set(type, new Set())
    this.#listeners.get(type)!.add(fn)
  }

  close() {
    /* no-op */
  }

  open() {
    this.onopen?.(new Event('open'))
  }

  emit(type: string, data: unknown) {
    const ev = { data: JSON.stringify(data) } as MessageEvent
    this.#listeners.get(type)?.forEach((fn) => fn(ev))
  }

  emitRaw(type: string, raw: string) {
    const ev = { data: raw } as MessageEvent
    this.#listeners.get(type)?.forEach((fn) => fn(ev))
  }
}

describe('createSSEClient fine-grained events', () => {
  afterEach(() => {
    MockEventSource.instances = []
    vi.useRealTimers()
  })

  function startClient() {
    const calls: Record<string, unknown[]> = {
      snapshot: [],
      counts: [],
      archive: [],
      scan: [],
      low_disk: [],
      metrics: [],
      heartbeat: [],
      event: [],
    }
    const client = createSSEClient(
      {
        onSnapshot: (d) => calls.snapshot.push(d),
        onCounts: (d) => calls.counts.push(d),
        onArchive: (d) => calls.archive.push(d),
        onScan: (d) => calls.scan.push(d),
        onLowDisk: (d) => calls.low_disk.push(d),
        onMetrics: (d) => calls.metrics.push(d),
        onHeartbeat: (d) => calls.heartbeat.push(d),
        onEvent: (name, d) => calls.event.push([name, d]),
      },
      {
        url: 'http://test/api/events',
        EventSourceImpl: MockEventSource as unknown as typeof EventSource,
      },
    )
    client.start()
    const es = MockEventSource.instances[0]
    expect(es).toBeTruthy()
    es.open()
    return { client, es, calls }
  }

  it('dispatches snapshot, counts, heartbeat', () => {
    const { client, es, calls } = startClient()
    es.emit('snapshot', { ok: true, archives: [] })
    es.emit('counts', { mounted: 1, counts: { mounted: 1 } })
    es.emit('heartbeat', { ts: 't0' })
    expect(calls.snapshot).toHaveLength(1)
    expect(calls.counts[0]).toMatchObject({ mounted: 1 })
    expect(calls.heartbeat[0]).toEqual({ ts: 't0' })
    client.stop()
  })

  it('dispatches archive, scan, low_disk, metrics', () => {
    const { client, es, calls } = startClient()
    es.emit('archive', {
      archives: [{ archive_id: 'a1', status: 'mounted' }],
      removed_ids: ['gone'],
    })
    es.emit('scan', { last_scan_at: '2026-01-01T00:00:00Z' })
    es.emit('low_disk', { low_disk: true, disk_free_bytes: 1 })
    es.emit('metrics', {
      metrics_summary: { total_space_saved_bytes: 99 },
    })

    expect(calls.archive[0]).toEqual({
      archives: [{ archive_id: 'a1', status: 'mounted' }],
      removed_ids: ['gone'],
    })
    expect(calls.scan[0]).toEqual({ last_scan_at: '2026-01-01T00:00:00Z' })
    expect(calls.low_disk[0]).toEqual({ low_disk: true, disk_free_bytes: 1 })
    expect(calls.metrics[0]).toEqual({
      metrics_summary: { total_space_saved_bytes: 99 },
    })

    const names = calls.event.map((e) => (e as [string, unknown])[0])
    expect(names).toEqual(['archive', 'scan', 'low_disk', 'metrics'])
    client.stop()
  })

  it('tolerates invalid JSON as { raw }', () => {
    const { client, es, calls } = startClient()
    es.emitRaw('archive', 'not-json')
    expect(calls.archive[0]).toEqual({ raw: 'not-json' })
    client.stop()
  })
})
