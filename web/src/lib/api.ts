/**
 * HTTP API client for mount-wrapper (/api/*).
 * Bearer token from window.__MOUNT_WRAPPER_TOKEN__ when the Go server injects it.
 */

import type {
  ActionResponse,
  ArchivesPayload,
  ConfigGetResponse,
  ConfigSetResponse,
  DoctorReport,
  HealthPayload,
  HooksListResponse,
  HooksRunResponse,
  HooksStatusResponse,
  RescanResponse,
  WSLInfo,
} from './types'

export type ApiError = Error & { status?: number; body?: unknown }

export function getToken(): string | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as Window & { __MOUNT_WRAPPER_TOKEN__?: string }).__MOUNT_WRAPPER_TOKEN__
}

export async function fetchJson<T = unknown>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers)
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  if (options.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const res = await fetch(path, { ...options, headers })
  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = { raw: text }
    }
  }
  if (!res.ok) {
    const err = new Error(
      (body && typeof body === 'object' && 'error' in body
        ? String((body as { error: unknown }).error)
        : res.statusText) || 'request failed',
    ) as ApiError
    err.status = res.status
    err.body = body
    throw err
  }
  return body as T
}

export function getHealth(): Promise<HealthPayload> {
  return fetchJson<HealthPayload>('/api/health')
}

export function getArchives(): Promise<ArchivesPayload> {
  return fetchJson<ArchivesPayload>('/api/archives')
}

export function getConfig(): Promise<ConfigGetResponse> {
  return fetchJson<ConfigGetResponse>('/api/config')
}

export function postConfig(body: {
  config?: Record<string, unknown>
  patch?: Record<string, unknown>
  apply?: boolean
}): Promise<ConfigSetResponse> {
  return fetchJson<ConfigSetResponse>('/api/config', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function postRescan(assumeStable = false): Promise<RescanResponse> {
  return fetchJson<RescanResponse>('/api/rescan', {
    method: 'POST',
    body: JSON.stringify({ assume_stable: assumeStable }),
  })
}

export function postUnmount(opts: {
  archive_id?: string
  target?: string
  all?: boolean
}): Promise<ActionResponse> {
  return fetchJson<ActionResponse>('/api/unmount', {
    method: 'POST',
    body: JSON.stringify(opts),
  })
}

export function postRetry(archiveId: string): Promise<ActionResponse> {
  return fetchJson<ActionResponse>('/api/retry', {
    method: 'POST',
    body: JSON.stringify({ archive_id: archiveId }),
  })
}

export function postPurge(archiveId: string): Promise<ActionResponse> {
  return fetchJson<ActionResponse>('/api/purge', {
    method: 'POST',
    body: JSON.stringify({ archive_id: archiveId, yes: true }),
  })
}

export function getDoctor(): Promise<DoctorReport> {
  return fetchJson<DoctorReport>('/api/doctor')
}

export function getWSLInfo(): Promise<WSLInfo> {
  return fetchJson<WSLInfo>('/api/wsl-info')
}

/** Per-archive hooks status (control op hooks_status). */
export function getHooksStatus(archiveId: string): Promise<HooksStatusResponse> {
  const q = new URLSearchParams({ archive_id: archiveId })
  return fetchJson<HooksStatusResponse>(`/api/hooks?${q.toString()}`)
}

/** Discovered hooks.d scripts (control op hooks_list). */
export function getHooksList(): Promise<HooksListResponse> {
  return fetchJson<HooksListResponse>('/api/hooks')
}

/** Run / re-run first-mount hooks (control op hooks_run). */
export function postHooksRun(
  archiveId: string,
  opts?: { force?: boolean },
): Promise<HooksRunResponse> {
  return fetchJson<HooksRunResponse>('/api/hooks', {
    method: 'POST',
    body: JSON.stringify({
      archive_id: archiveId,
      force: !!opts?.force,
    }),
  })
}

/** URL for EventSource; appends ?token= when configured (EventSource cannot set headers). */
export function eventsUrl(): string {
  const token = getToken()
  if (token) {
    return `/api/events?token=${encodeURIComponent(token)}`
  }
  return '/api/events'
}
