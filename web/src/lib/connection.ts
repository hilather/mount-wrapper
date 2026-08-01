/**
 * Pure helpers for the SPA connection badge (SSE vs poll fallback).
 */

import type { ConnectionStatus } from './types'

export interface ConnectionLabelInput {
  status: ConnectionStatus
  /** True when EventSource is open (SSE is the primary live path). */
  sseActive: boolean
  /**
   * When status is `service-down`, distinguish unreachable service vs hard API error.
   * - `service-down` → "service down"
   * - `error` → "error"
   */
  errorKind?: 'service-down' | 'error'
}

/** Short badge text. */
export function connectionLabel(input: ConnectionLabelInput): string {
  const { status, sseActive, errorKind } = input
  switch (status) {
    case 'connected':
      return sseActive ? 'live (SSE)' : 'poll (SSE down)'
    case 'reconnecting':
      return 'reconnecting'
    case 'service-down':
      return errorKind === 'error' ? 'error' : 'service down'
    case 'unknown':
    default:
      return '…'
  }
}

/** Tooltip / accessible description for the badge. */
export function connectionTitle(input: ConnectionLabelInput): string {
  const { status, sseActive, errorKind } = input
  switch (status) {
    case 'connected':
      return sseActive
        ? 'Live updates via Server-Sent Events (/api/events)'
        : 'Polling HTTP APIs; SSE is down or not yet connected'
    case 'reconnecting':
      return 'SSE reconnecting with backoff; HTTP poll fallback is active'
    case 'service-down':
      return errorKind === 'error'
        ? 'Could not reach the API (network or auth error)'
        : 'Control plane / service not reachable'
    case 'unknown':
    default:
      return 'Connection status unknown (SSE + poll fallback)'
  }
}
