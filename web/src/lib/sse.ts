/**
 * SSE client for GET /api/events with exponential backoff reconnect.
 * Uses EventSource; auth via ?token= when needed (see eventsUrl).
 *
 * Named events (server: internal/api/sse.go + sse_diff.go):
 *   snapshot | counts | archive | scan | low_disk | metrics | heartbeat
 */

import { eventsUrl } from './api'
import type {
  ArchiveSSEEvent,
  LowDiskSSEEvent,
  MetricsSSEEvent,
  ScanSSEEvent,
  StatusSnapshot,
} from './types'

export type SSEEventName =
  | 'snapshot'
  | 'counts'
  | 'archive'
  | 'scan'
  | 'low_disk'
  | 'metrics'
  | 'heartbeat'
  | string

export interface SSEHandlers {
  onSnapshot?: (data: StatusSnapshot) => void
  onCounts?: (data: Partial<StatusSnapshot>) => void
  /** Fine-grained row patch: { archives, removed_ids? }. */
  onArchive?: (data: ArchiveSSEEvent) => void
  /** last_scan_at (and optional scan meta) moved. */
  onScan?: (data: ScanSSEEvent) => void
  /** low_disk boolean edge. */
  onLowDisk?: (data: LowDiskSSEEvent) => void
  /** metrics_summary update (include_sizes path; may be null). */
  onMetrics?: (data: MetricsSSEEvent) => void
  onHeartbeat?: (data: unknown) => void
  onEvent?: (name: SSEEventName, data: unknown) => void
  onOpen?: () => void
  onError?: (err: unknown) => void
  onStatus?: (status: 'connecting' | 'open' | 'closed' | 'reconnecting') => void
}

export interface SSEClientOptions {
  /** Initial reconnect delay ms (default 1000). */
  baseDelayMs?: number
  /** Max reconnect delay ms (default 30000). */
  maxDelayMs?: number
  /** Optional URL override (tests). */
  url?: string
  /** EventSource factory (tests). */
  EventSourceImpl?: typeof EventSource
}

export interface SSEClient {
  start: () => void
  stop: () => void
  readonly connected: boolean
}

const NAMED_EVENTS = [
  'snapshot',
  'counts',
  'archive',
  'scan',
  'low_disk',
  'metrics',
  'heartbeat',
] as const

/**
 * Create a reconnecting SSE client. Call start() to connect; stop() to tear down permanently.
 */
export function createSSEClient(handlers: SSEHandlers, opts: SSEClientOptions = {}): SSEClient {
  const baseDelay = opts.baseDelayMs ?? 1000
  const maxDelay = opts.maxDelayMs ?? 30_000
  const ES = opts.EventSourceImpl ?? (typeof EventSource !== 'undefined' ? EventSource : undefined)

  let es: EventSource | null = null
  let stopped = false
  let attempt = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let connected = false

  function clearReconnect() {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function scheduleReconnect() {
    if (stopped) return
    clearReconnect()
    const delay = Math.min(maxDelay, baseDelay * Math.pow(2, attempt))
    attempt += 1
    handlers.onStatus?.('reconnecting')
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      connect()
    }, delay)
  }

  function parseData(raw: string): unknown {
    try {
      return JSON.parse(raw)
    } catch {
      return { raw }
    }
  }

  function dispatchNamed(name: (typeof NAMED_EVENTS)[number], data: unknown) {
    switch (name) {
      case 'snapshot':
        handlers.onSnapshot?.(data as StatusSnapshot)
        break
      case 'counts':
        handlers.onCounts?.(data as Partial<StatusSnapshot>)
        break
      case 'archive':
        handlers.onArchive?.(data as ArchiveSSEEvent)
        break
      case 'scan':
        handlers.onScan?.(data as ScanSSEEvent)
        break
      case 'low_disk':
        handlers.onLowDisk?.(data as LowDiskSSEEvent)
        break
      case 'metrics':
        handlers.onMetrics?.(data as MetricsSSEEvent)
        break
      case 'heartbeat':
        handlers.onHeartbeat?.(data)
        break
    }
    handlers.onEvent?.(name, data)
  }

  function connect() {
    if (stopped) return
    if (!ES) {
      handlers.onError?.(new Error('EventSource not available'))
      handlers.onStatus?.('closed')
      return
    }
    clearReconnect()
    handlers.onStatus?.(attempt === 0 ? 'connecting' : 'reconnecting')
    const url = opts.url ?? eventsUrl()
    try {
      es?.close()
    } catch {
      /* ignore */
    }
    es = new ES(url)

    es.onopen = () => {
      connected = true
      attempt = 0
      handlers.onOpen?.()
      handlers.onStatus?.('open')
    }

    es.onerror = () => {
      connected = false
      handlers.onError?.(new Error('SSE connection error'))
      try {
        es?.close()
      } catch {
        /* ignore */
      }
      es = null
      if (!stopped) scheduleReconnect()
    }

    for (const name of NAMED_EVENTS) {
      es.addEventListener(name, (ev) => {
        const data = parseData((ev as MessageEvent).data)
        dispatchNamed(name, data)
      })
    }

    // Fallback for unnamed messages.
    es.onmessage = (ev) => {
      const data = parseData(ev.data)
      handlers.onEvent?.('message', data)
    }
  }

  return {
    start() {
      stopped = false
      connect()
    },
    stop() {
      stopped = true
      clearReconnect()
      connected = false
      try {
        es?.close()
      } catch {
        /* ignore */
      }
      es = null
      handlers.onStatus?.('closed')
    },
    get connected() {
      return connected
    },
  }
}

/**
 * Compute next backoff delay (exported for unit tests).
 */
export function nextBackoffMs(attempt: number, baseDelayMs = 1000, maxDelayMs = 30_000): number {
  return Math.min(maxDelayMs, baseDelayMs * Math.pow(2, Math.max(0, attempt)))
}
