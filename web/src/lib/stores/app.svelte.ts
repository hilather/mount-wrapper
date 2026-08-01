/**
 * Shared app state (Svelte 5 runes module).
 * Archives overview, connection, config meta, toasts.
 */

import {
  getArchives,
  getHealth,
  getWSLInfo,
  type ApiError,
} from '../api'
import { mergeArchiveRows, patchArchiveRows } from '../merge'
import { createSSEClient, type SSEClient } from '../sse'
import type {
  ArchiveRow,
  ArchivesPayload,
  ArchiveSSEEvent,
  ConnectionStatus,
  Counts,
  LowDiskSSEEvent,
  MetricsSSEEvent,
  MetricsSummary,
  ScanSSEEvent,
  StatusSnapshot,
  Toast,
  ToastKind,
  WSLInfo,
} from '../types'

const POLL_MS = 15_000

let toastSeq = 0

class AppStore {
  // --- connection ---
  connectionStatus = $state<ConnectionStatus>('unknown')
  connectionLabel = $state('…')
  sseActive = $state(false)
  autoRefresh = $state(true)

  // --- archives / overview ---
  archives = $state<ArchiveRow[]>([])
  counts = $state<Counts>({})
  summary = $state<MetricsSummary | null>(null)
  version = $state<string>('')
  lowDisk = $state(false)
  lastScanAt = $state<string>('')
  lastRefreshAt = $state<string>('')
  loading = $state(false)
  initialLoadDone = $state(false)
  error = $state<string>('')
  serviceDownMessage = $state<string>('')
  rawPayload = $state<unknown>(null)

  // --- WSL ---
  wslInfo = $state<WSLInfo | null>(null)

  // --- toasts ---
  toasts = $state<Toast[]>([])

  // --- pending action ids (disable double-submit) ---
  pendingActions = $state<Record<string, boolean>>({})

  #sse: SSEClient | null = null
  #pollTimer: ReturnType<typeof setInterval> | null = null
  #started = false

  toast(kind: ToastKind, message: string) {
    const id = ++toastSeq
    this.toasts = [...this.toasts, { id, kind, message }]
    setTimeout(() => this.dismissToast(id), 8000)
  }

  dismissToast(id: number) {
    this.toasts = this.toasts.filter((t) => t.id !== id)
  }

  setPending(key: string, pending: boolean) {
    if (pending) {
      this.pendingActions = { ...this.pendingActions, [key]: true }
    } else {
      const next = { ...this.pendingActions }
      delete next[key]
      this.pendingActions = next
    }
  }

  isPending(key: string): boolean {
    return !!this.pendingActions[key]
  }

  applyOverview(data: Partial<ArchivesPayload | StatusSnapshot>) {
    const c = (data.counts as Counts | undefined) ?? {}
    this.counts = {
      mounted: data.mounted ?? c.mounted ?? 0,
      converting: (data as ArchivesPayload).converting ?? c.converting ?? 0,
      indexing: data.indexing ?? c.indexing ?? 0,
      mounting: data.mounting ?? c.mounting ?? 0,
      discovered: data.discovered ?? c.discovered ?? 0,
      hooks_running: data.hooks_running ?? c.hooks_running ?? 0,
      index_failed: data.index_failed ?? c.index_failed ?? 0,
      mount_failed: data.mount_failed ?? c.mount_failed ?? 0,
      absent: data.absent ?? c.absent ?? 0,
      ...c,
    }
    if (data.version != null) this.version = String(data.version)
    if (data.low_disk != null) this.lowDisk = !!data.low_disk
    if (data.last_scan_at != null) this.lastScanAt = String(data.last_scan_at)
  }

  applySnapshot(data: StatusSnapshot | ArchivesPayload) {
    if ('ok' in data && data.ok === false) {
      this.connectionStatus = 'service-down'
      this.connectionLabel = 'service down'
      this.serviceDownMessage = 'Service not reachable via control plane.'
      return
    }
    if (Array.isArray(data.archives)) {
      // SSE /status snapshots omit per-row metrics; preserve previous metrics by id.
      this.archives = mergeArchiveRows(this.archives, data.archives as ArchiveRow[])
    }
    this.applyOverview(data)
    // /api/archives has summary; plain status SSE has metrics_summary only when include_sizes.
    // Do not wipe a good summary when the snapshot omits it.
    const arch = data as ArchivesPayload
    const snap = data as StatusSnapshot
    if (arch.summary !== undefined && arch.summary !== null) {
      this.summary = arch.summary
    } else if (snap.metrics_summary !== undefined && snap.metrics_summary !== null) {
      this.summary = snap.metrics_summary
    }
    this.rawPayload = data
    this.lastRefreshAt = new Date().toLocaleTimeString()
    this.error = ''
    this.connectionStatus = 'connected'
    this.connectionLabel = 'connected'
    this.serviceDownMessage = ''
  }

  applyCounts(data: Partial<StatusSnapshot>) {
    this.applyOverview(data)
    this.lastRefreshAt = new Date().toLocaleTimeString()
    if (this.connectionStatus !== 'connected') {
      this.connectionStatus = 'connected'
      this.connectionLabel = 'connected'
    }
  }

  /**
   * Fine-grained SSE `archive` event: upsert/remove rows by archive_id without
   * wiping the table. Preserves per-row metrics when status patches omit them.
   */
  applyArchiveEvent(data: ArchiveSSEEvent) {
    const patches = Array.isArray(data?.archives) ? (data.archives as ArchiveRow[]) : []
    const removed = Array.isArray(data?.removed_ids) ? data.removed_ids : []
    if (patches.length === 0 && removed.length === 0) return
    this.archives = patchArchiveRows(this.archives, patches, removed)
    this.lastRefreshAt = new Date().toLocaleTimeString()
    if (this.connectionStatus !== 'connected') {
      this.connectionStatus = 'connected'
      this.connectionLabel = 'connected'
    }
  }

  /** SSE `scan` — update last_scan_at without full snapshot. */
  applyScanEvent(data: ScanSSEEvent) {
    if (data?.last_scan_at != null) {
      this.lastScanAt = String(data.last_scan_at)
      this.lastRefreshAt = new Date().toLocaleTimeString()
    }
  }

  /** SSE `low_disk` edge. */
  applyLowDiskEvent(data: LowDiskSSEEvent) {
    if (data?.low_disk != null) {
      this.lowDisk = !!data.low_disk
      this.lastRefreshAt = new Date().toLocaleTimeString()
    }
  }

  /**
   * SSE `metrics` — metrics_summary from include_sizes path.
   * Explicit null clears summary (server signals drop); undefined leaves it.
   */
  applyMetricsEvent(data: MetricsSSEEvent) {
    if (!data || !('metrics_summary' in data)) return
    this.summary = data.metrics_summary ?? null
    this.lastRefreshAt = new Date().toLocaleTimeString()
  }

  async refreshArchives(opts: { quiet?: boolean } = {}) {
    if (!opts.quiet) this.loading = true
    this.error = ''
    try {
      try {
        const health = await getHealth()
        if (!health.service_reachable) {
          this.connectionStatus = 'service-down'
          this.connectionLabel = 'service down'
          this.serviceDownMessage =
            'Service not reachable via control socket. Start: mount-wrapper serve'
        } else if (!this.sseActive) {
          this.connectionStatus = 'connected'
          this.connectionLabel = 'connected (poll)'
          this.serviceDownMessage = ''
        }
      } catch (e) {
        this.connectionStatus = 'service-down'
        this.connectionLabel = 'error'
        this.serviceDownMessage = String((e as Error).message || e)
      }

      const data = await getArchives()
      this.applySnapshot(data)
      this.initialLoadDone = true
      void this.loadWslHint()
    } catch (e) {
      const msg = String((e as ApiError).message || e)
      this.error = msg
      this.archives = []
      if (!this.sseActive) {
        this.connectionStatus = 'service-down'
        this.connectionLabel = 'error'
      }
    } finally {
      this.loading = false
      this.initialLoadDone = true
    }
  }

  async loadWslHint() {
    try {
      this.wslInfo = await getWSLInfo()
    } catch {
      this.wslInfo = null
    }
  }

  #startSSE() {
    this.#sse?.stop()
    this.#sse = createSSEClient({
      onOpen: () => {
        this.sseActive = true
        this.connectionStatus = 'connected'
        this.connectionLabel = 'connected'
        this.stopPoll()
      },
      onSnapshot: (data) => {
        this.sseActive = true
        this.applySnapshot(data)
        this.initialLoadDone = true
        // Occasional full /api/archives refresh keeps size metrics warm while SSE is live.
        this.#maybeMetricsRefresh()
      },
      onCounts: (data) => {
        this.applyCounts(data)
      },
      onArchive: (data) => {
        this.applyArchiveEvent(data)
      },
      onScan: (data) => {
        this.applyScanEvent(data)
      },
      onLowDisk: (data) => {
        this.applyLowDiskEvent(data)
      },
      onMetrics: (data) => {
        this.applyMetricsEvent(data)
      },
      onError: () => {
        this.sseActive = false
        if (this.connectionStatus === 'connected') {
          this.connectionStatus = 'reconnecting'
          this.connectionLabel = 'reconnecting'
        }
        this.startPoll()
      },
      onStatus: (st) => {
        if (st === 'reconnecting') {
          this.sseActive = false
          this.connectionStatus = 'reconnecting'
          this.connectionLabel = 'reconnecting'
          this.startPoll()
        } else if (st === 'open') {
          this.sseActive = true
          this.connectionStatus = 'connected'
          this.connectionLabel = 'connected'
        } else if (st === 'closed') {
          this.sseActive = false
        }
      },
    })
    this.#sse.start()
  }

  #lastMetricsAt = 0

  /** Soft-refresh metrics from /api/archives at most every POLL_MS while SSE is primary. */
  #maybeMetricsRefresh() {
    if (!this.autoRefresh) return
    const now = Date.now()
    if (now - this.#lastMetricsAt < POLL_MS) return
    this.#lastMetricsAt = now
    void this.refreshArchives({ quiet: true })
  }

  startPoll() {
    if (this.#pollTimer || !this.autoRefresh) return
    this.#pollTimer = setInterval(() => {
      if (this.autoRefresh) void this.refreshArchives({ quiet: true })
    }, POLL_MS)
  }

  stopPoll() {
    if (this.#pollTimer) {
      clearInterval(this.#pollTimer)
      this.#pollTimer = null
    }
  }

  setAutoRefresh(on: boolean) {
    this.autoRefresh = on
    if (!on) {
      this.stopPoll()
    } else if (!this.sseActive) {
      this.startPoll()
    }
  }

  /** Start live updates (SSE + poll fallback). Safe to call once from App. */
  start() {
    if (this.#started) return
    this.#started = true
    void this.refreshArchives().then(() => {
      this.#lastMetricsAt = Date.now()
    })
    this.#startSSE()
    // Poll until SSE proves healthy (or always as fallback when SSE fails).
    this.startPoll()
  }

  stop() {
    this.#started = false
    this.#sse?.stop()
    this.#sse = null
    this.stopPoll()
  }
}

export const app = new AppStore()
