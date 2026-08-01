import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatBytesCell,
  formatConvertDelta,
  formatDuration,
  formatDurationCell,
  formatNestedSkipsChip,
  formatNestedSkipsSubtitle,
  formatOriginalSize,
  formatStatusLabel,
  hasNestedSkips,
  isFailedStatus,
  isInProgressStatus,
} from './format'

describe('formatBytes', () => {
  it('returns null for missing or negative', () => {
    expect(formatBytes(null)).toBeNull()
    expect(formatBytes(undefined)).toBeNull()
    expect(formatBytes(NaN)).toBeNull()
    expect(formatBytes(-1)).toBeNull()
  })

  it('formats small and large sizes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    // exact power-of-two units use 2 digits when < 10 (upstream parity)
    expect(formatBytes(1024)).toBe('1.00 KiB')
    expect(formatBytes(1536)).toBe('1.50 KiB')
    expect(formatBytes(1024 * 1024)).toBe('1.00 MiB')
    expect(formatBytes(5 * 1024 * 1024 * 1024)).toBe('5.00 GiB')
    expect(formatBytes(10 * 1024)).toBe('10.0 KiB')
    expect(formatBytes(100 * 1024)).toBe('100 KiB')
  })

  it('formatBytesCell uses em dash', () => {
    expect(formatBytesCell(null)).toBe('—')
    expect(formatBytesCell(1024)).toBe('1.00 KiB')
  })
})

describe('formatDuration', () => {
  it('returns null for missing or negative', () => {
    expect(formatDuration(null)).toBeNull()
    expect(formatDuration(-3)).toBeNull()
  })

  it('formats seconds, minutes, hours', () => {
    expect(formatDuration(0)).toBe('0s')
    expect(formatDuration(45)).toBe('45s')
    expect(formatDuration(90)).toBe('1m 30s')
    expect(formatDuration(120)).toBe('2m')
    expect(formatDuration(3600)).toBe('1h')
    expect(formatDuration(3661)).toBe('1h 1m')
  })

  it('formatDurationCell uses em dash', () => {
    expect(formatDurationCell(undefined)).toBe('—')
    expect(formatDurationCell(30)).toBe('30s')
  })
})

describe('formatStatusLabel', () => {
  it('includes progress_label for in-progress statuses', () => {
    expect(
      formatStatusLabel({ status: 'indexing', progress_label: 'building index' }),
    ).toBe('indexing · building index')
    expect(
      formatStatusLabel({ status: 'converting', progress_label: 'converting to non-solid' }),
    ).toBe('converting · converting to non-solid')
    expect(formatStatusLabel({ status: 'mounting', progress_label: 'mounting FUSE' })).toBe(
      'mounting · mounting FUSE',
    )
  })

  it('returns bare status otherwise', () => {
    expect(formatStatusLabel({ status: 'mounted' })).toBe('mounted')
    expect(formatStatusLabel({ status: 'mounted', progress_label: 'x' })).toBe('mounted')
    expect(formatStatusLabel({})).toBe('—')
  })
})

describe('formatOriginalSize / convert delta', () => {
  it('shows em dash without original', () => {
    expect(formatOriginalSize({}, {})).toEqual({ base: '—', delta: null })
  })

  it('shows base without delta when equal', () => {
    expect(
      formatOriginalSize({ convert_source_size_bytes: 1000 }, { convert_size_delta_bytes: 0 }),
    ).toEqual({ base: '1000 B', delta: null })
  })

  it('shows signed delta', () => {
    const r = formatOriginalSize(
      { convert_source_size_bytes: 2048 },
      { convert_size_delta_bytes: 512 },
    )
    expect(r.base).toBe('2.00 KiB')
    expect(r.delta).toBe('+512 B')
  })

  it('derives delta from archive size when needed', () => {
    const r = formatOriginalSize(
      { convert_source_size_bytes: 1000 },
      { archive_size_bytes: 800 },
    )
    expect(r.delta).toBe('-200 B')
  })

  it('formatConvertDelta', () => {
    expect(formatConvertDelta(null)).toBeNull()
    expect(formatConvertDelta(0)).toBeNull()
    expect(formatConvertDelta(1024)).toBe('+1.00 KiB')
    expect(formatConvertDelta(-512)).toBe('-512 B')
  })
})

describe('status helpers', () => {
  it('isInProgressStatus / isFailedStatus', () => {
    expect(isInProgressStatus('indexing')).toBe(true)
    expect(isInProgressStatus('mounted')).toBe(false)
    expect(isFailedStatus('mount_failed')).toBe(true)
    expect(isFailedStatus('mounted')).toBe(false)
  })
})

describe('nested skips display', () => {
  it('hasNestedSkips', () => {
    expect(hasNestedSkips(null)).toBe(false)
    expect(hasNestedSkips({})).toBe(false)
    expect(hasNestedSkips({ nested_skips_count: 0 })).toBe(false)
    expect(hasNestedSkips({ nested_skips_count: 2 })).toBe(true)
    expect(hasNestedSkips({ nested_skips_summary: 'skipped 1 nested mount: /a' })).toBe(true)
  })

  it('formatNestedSkipsChip', () => {
    expect(formatNestedSkipsChip(null)).toBeNull()
    expect(formatNestedSkipsChip(0)).toBeNull()
    expect(formatNestedSkipsChip(1)).toBe('1 nested skip')
    expect(formatNestedSkipsChip(3)).toBe('3 nested skips')
  })

  it('formatNestedSkipsSubtitle prefers summary', () => {
    expect(
      formatNestedSkipsSubtitle({
        nested_skips_count: 2,
        nested_skips_summary: 'skipped 2 nested mounts: /a.7z, /b.7z',
      }),
    ).toBe('skipped 2 nested mounts: /a.7z, /b.7z')
    expect(formatNestedSkipsSubtitle({ nested_skips_count: 1 })).toBe('1 nested skip')
    expect(formatNestedSkipsSubtitle({})).toBeNull()
  })
})
