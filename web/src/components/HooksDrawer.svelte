<script lang="ts">
  import { getHooksStatus } from '../lib/api'
  import {
    formatHookRowSummary,
    getFocusableElements,
    hookStatusTone,
    sortHookRows,
  } from '../lib/hooks'
  import type { HooksStatusResponse } from '../lib/types'

  interface Props {
    /** Archive id to load; null/empty closes. */
    archiveId?: string | null
    archiveName?: string
    onclose?: () => void
  }
  let { archiveId = null, archiveName = '', onclose }: Props = $props()

  let panelEl: HTMLElement | undefined = $state()
  let closeBtn: HTMLButtonElement | undefined = $state()
  let loading = $state(false)
  let error = $state('')
  let data = $state<HooksStatusResponse | null>(null)
  let prevFocus: HTMLElement | null = null
  /** Drop stale responses when archiveId changes mid-flight. */
  let loadGen = 0

  const open = $derived(!!(archiveId && archiveId.trim()))
  const title = $derived(
    archiveName ? `Hooks · ${archiveName}` : archiveId ? `Hooks · ${archiveId}` : 'Hooks',
  )
  const rows = $derived(sortHookRows(data?.hooks))

  $effect(() => {
    if (!open || !archiveId) {
      loadGen += 1
      data = null
      error = ''
      return
    }
    const id = archiveId
    void load(id)
  })

  // Focus management + Escape when open.
  $effect(() => {
    if (!open) return
    prevFocus = (document.activeElement as HTMLElement) || null
    // Defer to after DOM paint so panelEl / closeBtn exist.
    const t = window.setTimeout(() => {
      closeBtn?.focus()
    }, 0)

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        close()
        return
      }
      if (e.key !== 'Tab' || !panelEl) return
      const focusables = getFocusableElements(panelEl)
      if (focusables.length === 0) {
        e.preventDefault()
        return
      }
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (e.shiftKey) {
        if (active === first || !panelEl.contains(active)) {
          e.preventDefault()
          last.focus()
        }
      } else if (active === last || !panelEl.contains(active)) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKey, true)
    return () => {
      window.clearTimeout(t)
      document.removeEventListener('keydown', onKey, true)
      if (prevFocus && typeof prevFocus.focus === 'function') {
        try {
          prevFocus.focus()
        } catch {
          /* ignore */
        }
      }
    }
  })

  async function load(id: string) {
    const gen = ++loadGen
    loading = true
    error = ''
    try {
      const next = await getHooksStatus(id)
      if (gen !== loadGen) return
      data = next
    } catch (e) {
      if (gen !== loadGen) return
      error = String((e as Error).message || e)
      data = null
    } finally {
      if (gen === loadGen) loading = false
    }
  }

  function close() {
    onclose?.()
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget) close()
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="drawer-backdrop"
    role="presentation"
    onclick={onBackdropClick}
  >
    <div
      class="hooks-drawer"
      bind:this={panelEl}
      role="dialog"
      aria-modal="true"
      aria-labelledby="hooks-drawer-title"
      tabindex="-1"
    >
      <div class="drawer-head">
        <h2 id="hooks-drawer-title">{title}</h2>
        <div class="toolbar drawer-tools">
          <button
            type="button"
            class="secondary tiny"
            disabled={loading || !archiveId}
            onclick={() => archiveId && load(archiveId)}
            aria-label="Refresh hooks status"
          >
            Refresh
          </button>
          <button
            type="button"
            class="secondary tiny"
            bind:this={closeBtn}
            onclick={close}
            aria-label="Close hooks drawer"
          >
            Close
          </button>
        </div>
      </div>

      <p class="hint drawer-meta">
        archive_id=<code>{archiveId}</code>
        {#if data?.hooks_status}
          · aggregate=<span class="hook-tone tone-{hookStatusTone(data.hooks_status)}"
            >{data.hooks_status}</span
          >
        {/if}
      </p>

      {#if loading && !data}
        <p class="loading" role="status">Loading hooks…</p>
      {:else if error}
        <p class="error" role="alert">{error}</p>
      {:else if rows.length === 0}
        <p class="hint empty-state">No per-hook rows recorded for this archive yet.</p>
      {:else}
        <div class="table-wrap hooks-table-wrap">
          <table class="hooks-detail">
            <thead>
              <tr>
                <th scope="col">Hook</th>
                <th scope="col">Status</th>
                <th class="num" scope="col">Attempts</th>
                <th class="num" scope="col">Exit</th>
                <th scope="col">Error</th>
              </tr>
            </thead>
            <tbody>
              {#each rows as h (h.hook_name)}
                <tr title={formatHookRowSummary(h)}>
                  <td class="name-cell">{h.hook_name || '—'}</td>
                  <td>
                    <span class="hook-tone tone-{hookStatusTone(h.status)}">{h.status || '—'}</span>
                  </td>
                  <td class="num">{h.attempts ?? '—'}</td>
                  <td class="num">
                    {h.last_exit_code != null && h.last_exit_code !== undefined
                      ? h.last_exit_code
                      : '—'}
                  </td>
                  <td class="path-sub error-cell">{h.last_error || '—'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </div>
{/if}
