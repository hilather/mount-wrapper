<script lang="ts">
  import { connectionTitle } from '../lib/connection'
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

  const title = $derived(
    connectionTitle({
      status: app.connectionStatus,
      sseActive: app.sseActive,
      errorKind: app.connectionErrorKind ?? undefined,
    }),
  )
</script>

<span
  class="badge {cls}"
  title={title}
  role="status"
  aria-live="polite"
  aria-label={title}
>
  {app.connectionLabel}
</span>
