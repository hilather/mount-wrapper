import { describe, expect, it } from 'vitest'
import {
  formatHookRowSummary,
  getFocusableElements,
  hookStatusTone,
  hooksStatusPath,
  sortHookRows,
} from './hooks'
import type { HookRow } from './types'

describe('hooksStatusPath', () => {
  it('encodes archive_id query', () => {
    expect(hooksStatusPath('abc')).toBe('/api/hooks?archive_id=abc')
    expect(hooksStatusPath('a b/c')).toBe('/api/hooks?archive_id=a%20b%2Fc')
  })

  it('falls back when empty', () => {
    expect(hooksStatusPath('')).toBe('/api/hooks')
    expect(hooksStatusPath('  ')).toBe('/api/hooks')
  })
})

describe('hookStatusTone', () => {
  it('maps known statuses', () => {
    expect(hookStatusTone('success')).toBe('ok')
    expect(hookStatusTone('failed')).toBe('bad')
    expect(hookStatusTone('retry')).toBe('warn')
    expect(hookStatusTone('running')).toBe('warn')
    expect(hookStatusTone('pending')).toBe('warn')
    expect(hookStatusTone('none')).toBe('muted')
    expect(hookStatusTone(undefined)).toBe('muted')
    expect(hookStatusTone('custom')).toBe('info')
  })
})

describe('formatHookRowSummary', () => {
  it('includes name status attempts exit', () => {
    const row: HookRow = {
      hook_name: 'notify.sh',
      status: 'success',
      attempts: 2,
      last_exit_code: 0,
    }
    expect(formatHookRowSummary(row)).toBe('notify.sh · success · attempts=2 · exit=0')
  })

  it('omits missing optional fields', () => {
    expect(formatHookRowSummary({ hook_name: 'x', status: 'pending' })).toBe('x · pending')
  })
})

describe('sortHookRows', () => {
  it('sorts by name case-insensitively', () => {
    const rows: HookRow[] = [
      { hook_name: 'z.sh', status: 'ok' },
      { hook_name: 'a.sh', status: 'ok' },
      { hook_name: 'M.sh', status: 'ok' },
    ]
    expect(sortHookRows(rows).map((r) => r.hook_name)).toEqual(['a.sh', 'M.sh', 'z.sh'])
  })

  it('handles empty', () => {
    expect(sortHookRows(null)).toEqual([])
    expect(sortHookRows([])).toEqual([])
  })
})

describe('getFocusableElements', () => {
  it('returns empty for null root', () => {
    expect(getFocusableElements(null)).toEqual([])
  })
})
