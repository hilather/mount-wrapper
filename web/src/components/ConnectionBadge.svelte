<script lang="ts">
  import { app } from '../lib/stores/app.svelte'

  const cls = $derived(
    app.connectionStatus === 'connected'
      ? 'ok'
      : app.connectionStatus === 'reconnecting'
        ? 'warn'
        : app.connectionStatus === 'service-down'
          ? 'bad'
          : 'unknown',
  )
</script>

<span class="badge {cls}" title="Live connection status (SSE + poll fallback)">
  {app.connectionLabel}
</span>
