import { describe, expect, it } from 'vitest'
import {
  mergePendingRestartKeys,
  PENDING_RESTART_STORAGE_KEY,
  readPendingRestartKeys,
  reconcilePendingRestartKeys,
  writePendingRestartKeys,
} from './pending-restart'

function memoryStorage(initial: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(initial))
  return {
    get length() {
      return map.size
    },
    clear() {
      map.clear()
    },
    getItem(key: string) {
      return map.has(key) ? (map.get(key) as string) : null
    },
    key(index: number) {
      return [...map.keys()][index] ?? null
    },
    removeItem(key: string) {
      map.delete(key)
    },
    setItem(key: string, value: string) {
      map.set(key, value)
    },
  }
}

describe('pending-restart', () => {
  it('merges Apply restart_required into sticky keys (sorted unique)', () => {
    expect(mergePendingRestartKeys(['web_port'], ['web_token', 'web_port'])).toEqual([
      'web_port',
      'web_token',
    ])
  })

  it('round-trips sessionStorage', () => {
    const storage = memoryStorage()
    writePendingRestartKeys(['web_token', 'web_host'], storage)
    expect(storage.getItem(PENDING_RESTART_STORAGE_KEY)).toContain('web_token')
    expect(readPendingRestartKeys(storage)).toEqual(['web_host', 'web_token'])
    writePendingRestartKeys([], storage)
    expect(readPendingRestartKeys(storage)).toEqual([])
  })

  it('reconcile drops keys no longer restart-classified', () => {
    expect(
      reconcilePendingRestartKeys(['web_token', 'log_level'], ['web_token', 'web_host']),
    ).toEqual(['web_token'])
  })

  it('reconcile keeps sticky when API omits restart_required_keys', () => {
    expect(reconcilePendingRestartKeys(['web_token'], [])).toEqual(['web_token'])
  })
})
