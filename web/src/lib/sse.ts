/**
 * SSE client for GET /api/events with exponential backoff reconnect.
 * Uses EventSource; auth via ?token= when needed (see eventsUrl).
 */

import { eventsUrl } from './api'
import type { StatusSnapshot } from './types'

export type SSEEventName = 'snapshot' | 'counts' | 'heartbeat' | string

export interface SSEHandlers {
  onSnapshot?: (data: StatusSnapshot) => void
  onCounts?: (data: Partial<StatusSnapshot>) => void
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

    es.addEventListener('snapshot', (ev) => {
      const data = parseData((ev as MessageEvent).data) as StatusSnapshot
      handlers.onSnapshot?.(data)
      handlers.onEvent?.('snapshot', data)
    })

    es.addEventListener('counts', (ev) => {
      const data = parseData((ev as MessageEvent).data) as Partial<StatusSnapshot>
      handlers.onCounts?.(data)
      handlers.onEvent?.('counts', data)
    })

    es.addEventListener('heartbeat', (ev) => {
      const data = parseData((ev as MessageEvent).data)
      handlers.onHeartbeat?.(data)
      handlers.onEvent?.('heartbeat', data)
    })

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
