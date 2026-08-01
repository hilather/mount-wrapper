/**
 * Pure helpers for per-archive hooks detail drawer.
 */

import type { HookRow } from './types'

/** Build GET /api/hooks URL for per-archive status (mirrors api.getHooksStatus). */
export function hooksStatusPath(archiveId: string): string {
  const id = (archiveId ?? '').trim()
  if (!id) return '/api/hooks'
  return `/api/hooks?archive_id=${encodeURIComponent(id)}`
}

/** CSS-ish status tone for chip styling. */
export type HookTone = 'ok' | 'bad' | 'warn' | 'info' | 'muted'

export function hookStatusTone(status: string | null | undefined): HookTone {
  const s = (status || '').toLowerCase()
  switch (s) {
    case 'success':
      return 'ok'
    case 'failed':
      return 'bad'
    case 'retry':
    case 'running':
    case 'pending':
      return 'warn'
    case 'none':
    case '':
      return 'muted'
    default:
      return 'info'
  }
}

/** Single-line summary for a hook row (name · status · attempts · exit). */
export function formatHookRowSummary(row: HookRow): string {
  const name = row.hook_name || '—'
  const status = row.status || '—'
  const parts = [`${name}`, status]
  if (row.attempts != null) {
    parts.push(`attempts=${row.attempts}`)
  }
  if (row.last_exit_code != null && row.last_exit_code !== undefined) {
    parts.push(`exit=${row.last_exit_code}`)
  }
  return parts.join(' · ')
}

/** Sort hooks by name for stable display. */
export function sortHookRows(rows: HookRow[] | null | undefined): HookRow[] {
  if (!rows?.length) return []
  return [...rows].sort((a, b) =>
    (a.hook_name || '').localeCompare(b.hook_name || '', undefined, { sensitivity: 'base' }),
  )
}

/** Collect focusable elements inside a container (for focus trap). */
export function getFocusableElements(root: HTMLElement | null | undefined): HTMLElement[] {
  if (!root) return []
  const sel =
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
  return Array.from(root.querySelectorAll<HTMLElement>(sel)).filter(
    (el) => !el.hasAttribute('disabled') && el.tabIndex !== -1 && el.offsetParent !== null,
  )
}
