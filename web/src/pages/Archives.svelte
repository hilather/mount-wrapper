<script lang="ts">
  import ArchivesTable from '../components/ArchivesTable.svelte'
  import DoctorPanel from '../components/DoctorPanel.svelte'
  import OverviewPills from '../components/OverviewPills.svelte'
  import SavingsBar from '../components/SavingsBar.svelte'
  import ToastStack from '../components/ToastStack.svelte'
  import { postRescan, postUnmount } from '../lib/api'
  import { app } from '../lib/stores/app.svelte'
  import { filterRows, sortRows, SORT_OPTIONS, STATUS_FILTER_OPTIONS } from '../lib/table'
  import type { SortKey } from '../lib/types'

  let filterStatus = $state('')
  let sortBy = $state<SortKey>('name')
  let sortDesc = $state(false)
  let doctorOpen = $state(false)
  let showRaw = $state(false)
  let globalBusy = $state(false)

  const rows = $derived(sortRows(filterRows(app.archives, filterStatus), sortBy, sortDesc))

  async function doRescan(assumeStable: boolean) {
    if (globalBusy) return
    if (assumeStable) {
      if (
        !window.confirm(
          'Rescan with assume-stable?\n\nThis bypasses the two-scan gate and may index incomplete downloads.',
        )
      ) {
        return
      }
    }
    globalBusy = true
    try {
      const data = await postRescan(assumeStable)
      app.toast(
        'ok',
        `Rescan done · seen=${data.seen ?? '?'} inserted=${data.inserted ?? '?'} stable=${data.stable ?? '?'}`,
      )
      await app.refreshArchives({ quiet: true })
    } catch (e) {
      app.toast('err', `Rescan failed: ${(e as Error).message || e}`)
    } finally {
      globalBusy = false
    }
  }

  async function doUnmountAll() {
    if (globalBusy) return
    if (!window.confirm('Unmount ALL managed archives?')) return
    globalBusy = true
    try {
      await postUnmount({ all: true })
      app.toast('ok', 'Unmount all requested')
      await app.refreshArchives({ quiet: true })
    } catch (e) {
      app.toast('err', `Unmount all failed: ${(e as Error).message || e}`)
    } finally {
      globalBusy = false
    }
  }

  async function copyUnc() {
    const unc = app.wslInfo?.unc_mounts
    if (!unc) return
    try {
      await navigator.clipboard.writeText(unc)
      app.toast('ok', 'Copied UNC path')
    } catch {
      app.toast('err', 'Clipboard write failed')
    }
  }
</script>

<div class="view">
  <section class="card" aria-labelledby="overview-heading">
    <div class="card-head">
      <h2 id="overview-heading">Overview</h2>
      <div class="toolbar global-actions">
        <button
          type="button"
          class="secondary"
          title="Immediate scan (stable gate still applies)"
          disabled={globalBusy}
          onclick={() => doRescan(false)}>Rescan</button
        >
        <button
          type="button"
          class="danger"
          title="Bypass stable-file gate"
          disabled={globalBusy}
          onclick={() => doRescan(true)}>Rescan (assume stable)</button
        >
        <button type="button" class="secondary" disabled={globalBusy} onclick={doUnmountAll}
          >Unmount all</button
        >
        <button type="button" class="secondary" onclick={() => (doctorOpen = true)}>Doctor</button>
      </div>
    </div>

    <OverviewPills />
    <SavingsBar />

    {#if app.wslInfo?.unc_mounts || app.wslInfo?.hint}
      <p class="wsl-hint hint">
        {#if app.wslInfo.unc_mounts}
          Windows UNC mounts: <code>{app.wslInfo.unc_mounts}</code>
          <button type="button" class="tiny secondary" onclick={copyUnc}>Copy</button>
          {#if app.wslInfo.distro_name}
            <span class="muted"> · distro={app.wslInfo.distro_name}</span>
          {/if}
        {:else}
          {app.wslInfo.hint}
        {/if}
      </p>
    {/if}

    <ToastStack />

    {#if app.serviceDownMessage}
      <p class="error" role="alert">{app.serviceDownMessage}</p>
    {/if}
    {#if app.lowDisk}
      <p class="banner banner-warn" role="alert">
        Low disk space under index/overlay paths. Run Doctor or free space before more indexing.
      </p>
    {/if}
  </section>

  <DoctorPanel open={doctorOpen} onclose={() => (doctorOpen = false)} />

  <section class="card" aria-labelledby="archives-heading">
    <div class="card-head">
      <h2 id="archives-heading">Archives</h2>
      <div class="toolbar">
        <label>
          Status
          <select bind:value={filterStatus}>
            {#each STATUS_FILTER_OPTIONS as opt}
              <option value={opt}>{opt === '' ? 'All' : opt}</option>
            {/each}
          </select>
        </label>
        <label>
          Sort
          <select bind:value={sortBy}>
            {#each SORT_OPTIONS as opt}
              <option value={opt.value}>{opt.label}</option>
            {/each}
          </select>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={sortDesc} />
          Desc
        </label>
        <button
          type="button"
          class="secondary"
          disabled={app.loading}
          onclick={() => app.refreshArchives()}>Refresh</button
        >
      </div>
    </div>

    {#if app.loading && !app.initialLoadDone}
      <p class="loading">Loading archives…</p>
    {:else if app.error && app.archives.length === 0}
      <p class="error" role="alert">{app.error}</p>
      <p class="hint empty-state">Unable to load archives. Check that serve is running and web is enabled.</p>
    {:else}
      <ArchivesTable {rows} />
    {/if}

    {#if app.lastRefreshAt}
      <p class="hint meta-line">updated {app.lastRefreshAt}</p>
    {/if}
  </section>

  <section class="card muted">
    <h2>Details</h2>
    <p class="hint formula-help">
      <strong>Space saved (vs extract)</strong> = extracted logical size − index size (index is the
      disk cost of mounting; archive may live elsewhere).<br />
      <strong>Original</strong> = pre-flatten 7z size when converted in place (delta vs current
      archive shows conversion cost).<br />
      <strong>Space saved (vs archive)</strong> = extracted − archive − index (mount footprint is
      archive + index on the same disk).
    </p>
    <button type="button" class="linkish" onclick={() => (showRaw = !showRaw)}>
      {showRaw ? 'Hide raw JSON' : 'Show raw JSON'}
    </button>
    {#if showRaw}
      <pre class="mono open">{JSON.stringify(app.rawPayload, null, 2)}</pre>
    {/if}
  </section>
</div>
