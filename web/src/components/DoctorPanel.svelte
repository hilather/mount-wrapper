<script lang="ts">
  import { getDoctor } from '../lib/api'
  import type { DoctorReport } from '../lib/types'

  interface Props {
    open?: boolean
    onclose?: () => void
  }
  let { open = false, onclose }: Props = $props()

  let loading = $state(false)
  let report = $state<DoctorReport | null>(null)
  let error = $state('')
  let showJson = $state(false)

  $effect(() => {
    if (open) {
      void run()
    }
  })

  async function run() {
    loading = true
    error = ''
    try {
      report = await getDoctor()
    } catch (e) {
      error = String((e as Error).message || e)
      report = null
    } finally {
      loading = false
    }
  }

  const checks = $derived(report?.checks ?? [])
  const bad = $derived(checks.filter((c) => !c.ok))
</script>

{#if open}
  <section class="card" aria-labelledby="doctor-heading">
    <div class="card-head">
      <h2 id="doctor-heading">Doctor</h2>
      <div class="toolbar">
        <button type="button" class="secondary" onclick={run} disabled={loading}>Re-run</button>
        <button type="button" class="linkish" onclick={() => onclose?.()}>Hide</button>
      </div>
    </div>
    {#if loading}
      <p class="loading">Running doctor…</p>
    {:else if error}
      <p class="error">{error}</p>
    {:else if report}
      <div class="summary">
        <span class="pill">
          {#if report.ok}
            <strong class="text-ok">OK</strong> · {checks.length} checks
          {:else}
            <strong class="text-bad">ISSUES</strong> · {bad.length} failing
          {/if}
        </span>
      </div>
      <pre class="mono open doctor-list"
        >{checks
          .map((c) => `${c.ok ? 'ok' : '!!'} [${c.severity ?? ''}] ${c.name}: ${c.message}`)
          .join('\n')}</pre
      >
      <button type="button" class="linkish" onclick={() => (showJson = !showJson)}>
        {showJson ? 'Hide raw JSON' : 'Show raw JSON'}
      </button>
      {#if showJson}
        <pre class="mono open">{JSON.stringify(report, null, 2)}</pre>
      {/if}
    {/if}
  </section>
{/if}
