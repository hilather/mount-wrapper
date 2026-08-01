/**
 * Client-side sticky list of config keys that need a process restart after Apply.
 *
 * web_* (including web_token) are captured at serve start and are never live-applied;
 * the Settings page surfaces them until the operator dismisses or clears after restart.
 *
 * sessionStorage keeps the banner across Validate/Reload and SPA navigations within
 * the same tab. Full process restart does not auto-clear (no server-side generation);
 * dismiss or an empty post-reload merge clears it.
 */

export const PENDING_RESTART_STORAGE_KEY = 'mount-wrapper.settings.pendingRestartKeys'

export function readPendingRestartKeys(
  storage: Pick<Storage, 'getItem'> | null = defaultSessionStorage(),
): string[] {
  if (!storage) return []
  try {
    const raw = storage.getItem(PENDING_RESTART_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return normalizeKeys(parsed.filter((k): k is string => typeof k === 'string'))
  } catch {
    return []
  }
}

export function writePendingRestartKeys(
  keys: string[],
  storage: Pick<Storage, 'setItem' | 'removeItem'> | null = defaultSessionStorage(),
): void {
  if (!storage) return
  const normalized = normalizeKeys(keys)
  try {
    if (normalized.length === 0) {
      storage.removeItem(PENDING_RESTART_STORAGE_KEY)
    } else {
      storage.setItem(PENDING_RESTART_STORAGE_KEY, JSON.stringify(normalized))
    }
  } catch {
    /* private mode / quota — ignore */
  }
}

/** Union previous sticky keys with keys returned from a successful Apply. */
export function mergePendingRestartKeys(previous: string[], fromApply: string[]): string[] {
  return normalizeKeys([...previous, ...fromApply])
}

/**
 * After Reload from service: drop sticky keys that are no longer classified as
 * restart-required by the API (e.g. schema change). Keys still in
 * restart_required_keys stay pending until dismiss or process restart.
 */
export function reconcilePendingRestartKeys(
  pending: string[],
  restartRequiredKeys: string[],
): string[] {
  if (pending.length === 0) return []
  if (restartRequiredKeys.length === 0) {
    // API omitted classification — keep sticky list as-is.
    return normalizeKeys(pending)
  }
  const allowed = new Set(restartRequiredKeys)
  return normalizeKeys(pending.filter((k) => allowed.has(k)))
}

function normalizeKeys(keys: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const k of keys) {
    const key = String(k).trim()
    if (!key || seen.has(key)) continue
    seen.add(key)
    out.push(key)
  }
  out.sort()
  return out
}

function defaultSessionStorage(): Storage | null {
  try {
    if (typeof sessionStorage === 'undefined') return null
    return sessionStorage
  } catch {
    return null
  }
}
