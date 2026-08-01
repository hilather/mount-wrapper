<script lang="ts">
  import {
    formatBytes,
    formatBytesCell,
    formatDuration,
    formatDurationCell,
    formatNestedSkipsChip,
    formatNestedSkipsSubtitle,
    formatOriginalSize,
    formatStatusLabel,
    hasNestedSkips,
    isFailedStatus,
    isInProgressStatus,
  } from '../lib/format'
  import { hookStatusTone } from '../lib/hooks'
  import { postPurge, postRetry, postUnmount } from '../lib/api'
  import { app } from '../lib/stores/app.svelte'
  import type { ArchiveRow } from '../lib/types'
  import HooksDrawer from './HooksDrawer.svelte'

  interface Props {
    rows: ArchiveRow[]
  }
  let { rows }: Props = $props()

  let hooksArchiveId = $state<string | null>(null)
  let hooksArchiveName = $state('')

  function openHooks(r: ArchiveRow) {
    hooksArchiveId = r.archive_id
    hooksArchiveName = r.archive_basename || r.archive_id
  }

  function closeHooks() {
    hooksArchiveId = null
    hooksArchiveName = ''
  }

  async function copyPath(text: string) {
    if (!text) {
      app.toast('warn', 'No path to copy')
      return
    }
    try {
      await navigator.clipboard.writeText(text)
      app.toast('ok', `Copied ${text}`)
    } catch {
      app.toast('err', 'Clipboard write failed')
    }
  }

  async function onRetry(id: string) {
    const key = `retry:${id}`
    if (app.isPending(key)) return
    app.setPending(key, true)
    try {
      const data = await postRetry(id)
      app.toast('ok', `Retry queued for ${id} · status=${data.status ?? '?'}`)
      await app.refreshArchives({ quiet: true })
    } catch (e) {
      app.toast('err', `Retry failed: ${(e as Error).message || e}`)
    } finally {
      app.setPending(key, false)
    }
  }

  async function onUnmount(id: string) {
    if (!window.confirm(`Unmount archive ${id}?`)) return
    const key = `unmount:${id}`
    if (app.isPending(key)) return
    app.setPending(key, true)
    try {
      await postUnmount({ archive_id: id })
      app.toast('ok', `Unmount requested for ${id}`)
      await app.refreshArchives({ quiet: true })
    } catch (e) {
      app.toast('err', `Unmount failed: ${(e as Error).message || e}`)
    } finally {
      app.setPending(key, false)
    }
  }

  async function onPurge(id: string, name: string) {
    if (
      !window.confirm(
        `PURGE archive "${name}"?\n\nThis unmounts, removes index, handles overlay per policy, and deletes DB state.\nThis cannot be undone.`,
      )
    ) {
      return
    }
    const key = `purge:${id}`
    if (app.isPending(key)) return
    app.setPending(key, true)
    try {
      const data = await postPurge(id)
      app.toast('ok', `Purged ${id} · overlay=${data.overlay_action ?? '?'}`)
      await app.refreshArchives({ quiet: true })
    } catch (e) {
      app.toast('err', `Purge failed: ${(e as Error).message || e}`)
    } finally {
      app.setPending(key, false)
    }
  }

  function rowClass(r: ArchiveRow): string {
    if (isInProgressStatus(r.status)) return 'row-indexing'
    if (isFailedStatus(r.status)) return 'row-failed'
    return ''
  }

  function indexTime(r: ArchiveRow): string {
    if (r.index_duration_seconds != null) return formatDurationCell(r.index_duration_seconds)
    if (r.status === 'indexing' && r.elapsed_s != null)
      return `${formatDuration(r.elapsed_s) ?? Math.round(r.elapsed_s) + 's'}…`
    return '—'
  }

  function mountTime(r: ArchiveRow): string {
    if (r.mount_duration_seconds != null) return formatDurationCell(r.mount_duration_seconds)
    if (r.status === 'mounting' && r.elapsed_s != null)
      return `${formatDuration(r.elapsed_s) ?? Math.round(r.elapsed_s) + 's'}…`
    return '—'
  }

  function convertTime(r: ArchiveRow): string {
    const m = r.metrics || {}
    const d = r.convert_duration_seconds ?? m.convert_duration_seconds
    if (d != null) return formatDurationCell(d)
    if (r.status === 'converting' && r.elapsed_s != null)
      return `${formatDuration(r.elapsed_s) ?? Math.round(r.elapsed_s) + 's'}…`
    return '—'
  }
</script>

{#if rows.length === 0}
  <p class="hint empty-state">No archives match this filter.</p>
{:else}
  <div class="table-wrap">
    <table class="archives">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Status</th>
          <th scope="col">Hooks</th>
          <th class="num" scope="col">Archive</th>
          <th class="num" scope="col">Original</th>
          <th class="num" scope="col">Convert time</th>
          <th class="num" scope="col">Index</th>
          <th class="num" scope="col">Index time</th>
          <th class="num" scope="col">Mount time</th>
          <th class="num" scope="col">Extracted</th>
          <th class="num" scope="col" title="extracted − index">Saved (vs extract)</th>
          <th class="num" scope="col" title="extracted − archive − index">Saved (vs archive)</th>
          <th scope="col">Mount / path</th>
          <th scope="col">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as r (r.archive_id)}
          {@const m = r.metrics || {}}
          {@const orig = formatOriginalSize(r, m)}
          {@const mount = r.mount_path || ''}
          {@const path = r.archive_path || ''}
          {@const copyTarget = mount || path || ''}
          {@const saved = m.space_saved_bytes}
          <tr class={rowClass(r)}>
            <td class="name-cell">
              {r.archive_basename || '—'}
              {#if isInProgressStatus(r.status) && r.elapsed_s != null}
                <span class="elapsed">{Math.round(r.elapsed_s)}s elapsed</span>
              {/if}
            </td>
            <td>
              <span class="status-chip {r.status || ''}">{formatStatusLabel(r)}</span>
              {#if hasNestedSkips(r)}
                {@const skipChip = formatNestedSkipsChip(r.nested_skips_count)}
                {@const skipSub = formatNestedSkipsSubtitle(r)}
                {#if skipChip}
                  <span class="status-chip nested-skips" title={skipSub || skipChip}>{skipChip}</span>
                {/if}
              {/if}
              {#if r.last_error && isFailedStatus(r.status)}
                <div class="path-sub" title={r.last_error}>{r.last_error}</div>
              {:else if hasNestedSkips(r)}
                {@const skipSub = formatNestedSkipsSubtitle(r)}
                {#if skipSub}
                  <div class="path-sub nested-skips-sub" title={skipSub}>{skipSub}</div>
                {/if}
              {/if}
            </td>
            <td>
              <button
                type="button"
                class="linkish hooks-cell tone-{hookStatusTone(r.hooks_status)}"
                title="View per-hook status"
                aria-label={`Hooks for ${r.archive_basename || r.archive_id}: ${r.hooks_status || 'none'}`}
                onclick={() => openHooks(r)}
              >
                {r.hooks_status || '—'}
              </button>
            </td>
            <td class="num">{formatBytesCell(m.archive_size_bytes)}</td>
            <td class="num">
              {orig.base}
              {#if orig.delta}
                <div class="convert-delta">{orig.delta}</div>
              {/if}
            </td>
            <td class="num">
              {#if convertTime(r).endsWith('…')}
                <span class="elapsed">{convertTime(r)}</span>
              {:else}
                {convertTime(r)}
              {/if}
            </td>
            <td class="num">{formatBytesCell(m.index_size_bytes)}</td>
            <td class="num">
              {#if indexTime(r).endsWith('…')}
                <span class="elapsed">{indexTime(r)}</span>
              {:else}
                {indexTime(r)}
              {/if}
            </td>
            <td class="num">
              {#if mountTime(r).endsWith('…')}
                <span class="elapsed">{mountTime(r)}</span>
              {:else}
                {mountTime(r)}
              {/if}
            </td>
            <td class="num">{formatBytesCell(m.extracted_size_bytes)}</td>
            <td class="num">
              {#if saved == null}
                —
              {:else if saved > 0}
                <span class="saved-pos">{formatBytes(saved)}</span>
              {:else}
                {formatBytesCell(saved)}
              {/if}
            </td>
            <td class="num">{formatBytesCell(m.space_saved_vs_archive_bytes)}</td>
            <td class="path-cell">
              {#if mount}
                <div>{mount}</div>
                <div class="path-sub">{path}</div>
              {:else}
                {path || '—'}
              {/if}
            </td>
            <td>
              <div class="row-actions">
                <button
                  type="button"
                  class="tiny secondary"
                  title="Copy path"
                  onclick={() => copyPath(copyTarget)}>Copy</button
                >
                <button
                  type="button"
                  class="tiny secondary"
                  title="Per-hook status"
                  aria-label={`Open hooks detail for ${r.archive_basename || r.archive_id}`}
                  onclick={() => openHooks(r)}>Hooks</button
                >
                <button
                  type="button"
                  class="tiny secondary"
                  disabled={app.isPending(`retry:${r.archive_id}`)}
                  onclick={() => onRetry(r.archive_id)}>Retry</button
                >
                <button
                  type="button"
                  class="tiny secondary"
                  disabled={app.isPending(`unmount:${r.archive_id}`)}
                  onclick={() => onUnmount(r.archive_id)}>Unmount</button
                >
                <button
                  type="button"
                  class="tiny danger"
                  disabled={app.isPending(`purge:${r.archive_id}`)}
                  onclick={() => onPurge(r.archive_id, r.archive_basename || r.archive_id)}
                  >Purge</button
                >
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<HooksDrawer
  archiveId={hooksArchiveId}
  archiveName={hooksArchiveName}
  onclose={closeHooks}
/>
