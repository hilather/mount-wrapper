<script lang="ts">
  import { formatBytes, formatDuration } from '../lib/format'
  import { app } from '../lib/stores/app.svelte'

  const s = $derived(app.summary)
</script>

{#if s}
  <div class="savings-bar" title="Aggregated space-saved metrics from /api/archives summary">
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
{/if}
