<script lang="ts">
  import { formatBytes, formatDuration } from '../lib/format'
  import { app } from '../lib/stores/app.svelte'

  const s = $derived(app.summary)
</script>

{#if s}
  <div class="savings-wrap" title="Aggregated size and mount memory metrics from /api/archives summary">
    <div class="savings-bar">
      <span
        >Space saved (vs extract):
        <span class="big">{formatBytes(s.total_space_saved_bytes) ?? '—'}</span></span
      >
      <span>Extracted total: <strong>{formatBytes(s.total_extracted_size_bytes) ?? '—'}</strong></span>
      <span>Indexes total: <strong>{formatBytes(s.total_index_size_bytes) ?? '—'}</strong></span>
      <span>Archives on disk: <strong>{formatBytes(s.total_archive_size_bytes) ?? '—'}</strong></span>
      <span
        >Original total: <strong>{formatBytes(s.total_convert_source_size_bytes) ?? '—'}</strong></span
      >
      <span
        >Convert delta: <strong>{formatBytes(s.total_convert_size_delta_bytes) ?? '—'}</strong></span
      >
      <span
        >Longest convert: <strong
          >{formatDuration(s.max_convert_duration_seconds) ?? '—'}</strong
        ></span
      >
      <span>Sized: {s.archives_with_extracted_size ?? 0}/{s.archive_count ?? 0}</span>
      <span>Converted: {s.archives_with_convert_metadata ?? 0}/{s.archive_count ?? 0}</span>
      <span
        >Convert timed: {s.archives_with_convert_duration ?? 0}/{s.archive_count ?? 0}</span
      >
    </div>
    <div
      class="savings-bar savings-bar-mem"
      title="Sum of FUSE child process RSS (resident set) for live mount_pid samples"
    >
      <span
        >Mount memory (RSS):
        <span class="big">{formatBytes(s.total_mount_rss_bytes) ?? '—'}</span></span
      >
      {#if s.total_mount_rss_peak_bytes}
        <span
          >Peak RSS total: <strong>{formatBytes(s.total_mount_rss_peak_bytes) ?? '—'}</strong></span
        >
      {/if}
      <span
        >Mounts sampled: {s.archives_with_mount_rss ?? 0}/{s.archive_count ?? 0}</span
      >
    </div>
  </div>
{/if}
