<script lang="ts">
  import { app } from '../lib/stores/app.svelte'

  const keys = [
    'mounted',
    'converting',
    'indexing',
    'mounting',
    'discovered',
    'hooks_running',
    'index_failed',
    'mount_failed',
    'absent',
  ] as const
</script>

<div class="summary" role="status">
  {#each keys as k}
    {@const n = app.counts[k] ?? 0}
    {#if n > 0 || k === 'mounted' || k === 'indexing'}
      <span class="pill">{k}: <strong>{n}</strong></span>
    {/if}
  {/each}
  {#if app.version}
    <span class="pill">version: <strong>{app.version}</strong></span>
  {/if}
  {#if app.lowDisk}
    <span class="pill pill-danger"><strong>low disk</strong></span>
  {/if}
</div>
