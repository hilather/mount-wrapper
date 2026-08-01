<script lang="ts">
  import { getConfig, postConfig } from '../lib/api'
  import {
    mergePendingRestartKeys,
    readPendingRestartKeys,
    reconcilePendingRestartKeys,
    writePendingRestartKeys,
  } from '../lib/pending-restart'
  import {
    SETTINGS_SCHEMA,
    destructiveWarnings,
    formToValue,
    valueToForm,
    type SettingsField,
  } from '../lib/settings-schema'
  import type { ConfigGetResponse } from '../lib/types'

  let loading = $state(true)
  let busy = $state(false)
  let error = $state('')
  /** Transient Validate/Apply status (cleared on next action / Reload). */
  let banner = $state<{ kind: string; html: string } | null>(null)
  /**
   * Sticky restart-required keys from Apply (esp. web_*). Survives Validate /
   * Reload until Dismiss or reconcile removes all keys. sessionStorage optional.
   */
  let pendingRestartKeys = $state<string[]>(readPendingRestartKeys())
  let meta = $state('')
  let formValues = $state<Record<string, string | boolean>>({})
  let hotKeys = $state<Set<string>>(new Set())
  let restartKeys = $state<Set<string>>(new Set())
  let configPath = $state('')

  $effect(() => {
    void load()
  })

  function setPendingRestartKeys(keys: string[]) {
    pendingRestartKeys = keys
    writePendingRestartKeys(keys)
  }

  function dismissPendingRestart() {
    setPendingRestartKeys([])
  }

  function initForm(config: Record<string, unknown>, metaResp?: ConfigGetResponse) {
    const next: Record<string, string | boolean> = {}
    for (const group of SETTINGS_SCHEMA) {
      for (const field of group.fields) {
        next[field.key] = valueToForm(field, config[field.key])
      }
    }
    formValues = next
    const hot = metaResp?.hot_reload_keys ?? []
    const restart = metaResp?.restart_required_keys ?? []
    hotKeys = new Set(hot)
    restartKeys = new Set(restart)
    configPath = metaResp?.config_path ?? ''
    meta = `Loaded from service · ${configPath || 'unknown'} · ${new Date().toLocaleString()}`
    // Drop sticky keys the service no longer classifies as restart-required.
    if (pendingRestartKeys.length && restart.length) {
      const reconciled = reconcilePendingRestartKeys(pendingRestartKeys, restart)
      if (reconciled.join('\0') !== pendingRestartKeys.join('\0')) {
        setPendingRestartKeys(reconciled)
      }
    }
  }

  async function load() {
    loading = true
    error = ''
    banner = null
    // Do not clear pendingRestartKeys — sticky until dismiss / reconcile.
    try {
      const data = await getConfig()
      const config = (data.config ?? data.values ?? {}) as Record<string, unknown>
      initForm(config, data)
    } catch (e) {
      error = String((e as Error).message || e)
    } finally {
      loading = false
    }
  }

  function collect(): Record<string, unknown> {
    const out: Record<string, unknown> = {}
    for (const group of SETTINGS_SCHEMA) {
      for (const field of group.fields) {
        const raw = formValues[field.key]
        out[field.key] = formToValue(field, raw ?? (field.type === 'bool' ? false : ''))
      }
    }
    return out
  }

  function fieldIsRestart(field: SettingsField): boolean {
    return !!field.restart || restartKeys.has(field.key)
  }

  async function submit(apply: boolean) {
    if (busy) return
    banner = null
    error = ''
    let config: Record<string, unknown>
    try {
      config = collect()
    } catch (e) {
      error = String((e as Error).message || e)
      return
    }

    if (apply) {
      const warns = destructiveWarnings(config)
      if (warns.length) {
        if (!window.confirm('Destructive or risky settings:\n\n• ' + warns.join('\n• ') + '\n\nApply anyway?')) {
          return
        }
      }
    }

    busy = true
    try {
      const result = await postConfig({ config, apply })
      const changed = (result.changed_keys || []).join(', ') || '(none)'
      const hot = (result.hot_reloadable || []).join(', ') || '(none)'
      const restartList = result.restart_required || []
      const restart = restartList.join(', ') || '(none)'
      if (!apply) {
        banner = {
          kind: 'warn',
          html: `<strong>Validation OK (dry-run)</strong><br/>Changed: ${esc(changed)}<br/>Hot: ${esc(hot)}<br/>Restart required: ${esc(restart)}`,
        }
        return
      }
      let kind = 'ok'
      let extra = ''
      if (restartList.length) {
        kind = 'warn'
        extra = `<br/><strong>Restart required</strong> for: ${esc(restart)}`
        // Sticky banner (esp. web_token / web_* — never live-applied).
        setPendingRestartKeys(mergePendingRestartKeys(pendingRestartKeys, restartList))
      }
      banner = {
        kind,
        html: `<strong>Applied</strong> at ${new Date().toLocaleString()} · written=${!!result.written} · reloaded=${!!result.reloaded}<br/>Changed: ${esc(changed)}${extra}`,
      }
      if (result.config) {
        initForm(result.config as Record<string, unknown>, {
          config: result.config,
          config_path: configPath,
          hot_reload_keys: [...hotKeys],
          restart_required_keys: [...restartKeys],
        })
      }
    } catch (e) {
      const body = (e as { body?: { error?: string } }).body
      const detail = body?.error || (e as Error).message || String(e)
      banner = { kind: 'err', html: `<strong>Failed</strong>: ${esc(detail)}` }
    } finally {
      busy = false
    }
  }

  function esc(s: string): string {
    return s
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
  }
</script>

<div class="view">
  <section class="card" aria-labelledby="settings-heading">
    <div class="card-head">
      <h2 id="settings-heading">Settings</h2>
      <div class="toolbar settings-actions">
        <button type="button" class="secondary" disabled={loading || busy} onclick={load}
          >Reload from service</button
        >
        <button type="button" class="secondary" disabled={loading || busy} onclick={() => submit(false)}
          >Validate</button
        >
        <button type="button" disabled={loading || busy} onclick={() => submit(true)}>Apply</button>
      </div>
    </div>

    <p class="hint">{meta || 'Load configuration from the running service.'}</p>

    {#if pendingRestartKeys.length}
      <div
        class="banner banner-warn banner-restart-sticky"
        role="status"
        data-testid="restart-required-banner"
      >
        <div class="banner-restart-body">
          <strong>Process restart required</strong>
          <br />
          Keys written but not yet live in this process:
          <code>{pendingRestartKeys.join(', ')}</code>
          <br />
          <span class="banner-restart-note"
            >web_* (including <code>web_token</code>) are captured at serve start — restart
            <code>mount-wrapper</code>; they are not live-applied.</span
          >
        </div>
        <button type="button" class="secondary" data-testid="restart-required-dismiss" onclick={dismissPendingRestart}
          >Dismiss</button
        >
      </div>
    {/if}

    {#if banner}
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      <div class="banner banner-{banner.kind}">{@html banner.html}</div>
    {/if}
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    {#if loading}
      <p class="loading">Loading settings…</p>
    {:else}
      <form
        class="settings-form"
        onsubmit={(e) => {
          e.preventDefault()
          void submit(true)
        }}
      >
        {#each SETTINGS_SCHEMA as group}
          <section class="settings-group" aria-labelledby="sg-{group.id}">
            <h3 id="sg-{group.id}">{group.title}</h3>
            {#each group.fields as field}
              {@const restart = fieldIsRestart(field)}
              <div class="field" class:restart>
                <label for="cfg-{field.key}">
                  {field.label}
                  {#if restart}
                    <span class="tag-restart" title="Restart required for this key">restart</span>
                  {/if}
                  {#if field.destructive}
                    <span class="tag-danger" title="Destructive option">caution</span>
                  {/if}
                </label>
                <div class="control" class:checkbox={field.type === 'bool'}>
                  {#if field.type === 'bool'}
                    <input
                      id="cfg-{field.key}"
                      type="checkbox"
                      checked={!!formValues[field.key]}
                      onchange={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLInputElement).checked,
                        }
                      }}
                    />
                  {:else if field.type === 'select'}
                    <select
                      id="cfg-{field.key}"
                      value={String(formValues[field.key] ?? '')}
                      onchange={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLSelectElement).value,
                        }
                      }}
                    >
                      {#each field.options || [] as opt}
                        <option value={opt}>{opt}</option>
                      {/each}
                    </select>
                  {:else if field.type === 'string_list'}
                    <textarea
                      id="cfg-{field.key}"
                      rows="3"
                      value={String(formValues[field.key] ?? '')}
                      oninput={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLTextAreaElement).value,
                        }
                      }}
                    ></textarea>
                  {:else if field.type === 'password'}
                    <input
                      id="cfg-{field.key}"
                      type="password"
                      autocomplete="off"
                      value={String(formValues[field.key] ?? '')}
                      oninput={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLInputElement).value,
                        }
                      }}
                    />
                  {:else if field.type === 'int' || field.type === 'number'}
                    <input
                      id="cfg-{field.key}"
                      type="number"
                      step={field.type === 'number' ? 'any' : '1'}
                      value={String(formValues[field.key] ?? '')}
                      oninput={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLInputElement).value,
                        }
                      }}
                    />
                  {:else}
                    <input
                      id="cfg-{field.key}"
                      type="text"
                      value={String(formValues[field.key] ?? '')}
                      oninput={(e) => {
                        formValues = {
                          ...formValues,
                          [field.key]: (e.currentTarget as HTMLInputElement).value,
                        }
                      }}
                    />
                  {/if}
                </div>
                {#if field.help}
                  <div class="help">{field.help}</div>
                {/if}
              </div>
            {/each}
          </section>
        {/each}
      </form>
      <p class="hint settings-footnote">
        Changes write <code>config.yaml</code> via the service control plane. Hot-reloadable keys
        apply immediately; restart-required keys need
        <code>systemctl restart mount-wrapper</code> (or equivalent).
        <code>web_token</code> and other <code>web_*</code> keys are never live-applied.
      </p>
    {/if}
  </section>
</div>
