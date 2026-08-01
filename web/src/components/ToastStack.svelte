<script lang="ts">
  import { app } from '../lib/stores/app.svelte'
</script>

{#if app.toasts.length}
  <div class="toast-stack" aria-live="polite">
    {#each app.toasts as t (t.id)}
      <div class="banner toast banner-{t.kind}" role="status">
        <span>{t.message}</span>
        <button type="button" class="linkish" onclick={() => app.dismissToast(t.id)} aria-label="Dismiss"
          >×</button
        >
      </div>
    {/each}
  </div>
{/if}
