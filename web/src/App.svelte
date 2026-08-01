<script lang="ts">
  import ConnectionBadge from './components/ConnectionBadge.svelte'
  import Archives from './pages/Archives.svelte'
  import Settings from './pages/Settings.svelte'
  import { app } from './lib/stores/app.svelte'

  type View = 'archives' | 'settings'

  let view = $state<View>('archives')
  let theme = $state<'light' | 'dark'>(
    typeof localStorage !== 'undefined' && localStorage.getItem('mw-theme') === 'dark'
      ? 'dark'
      : typeof localStorage !== 'undefined' && localStorage.getItem('mw-theme') === 'light'
        ? 'light'
        : matchMediaPrefersDark()
          ? 'dark'
          : 'light',
  )

  function matchMediaPrefersDark(): boolean {
    try {
      return typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches
    } catch {
      return false
    }
  }

  $effect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem('mw-theme', theme)
    } catch {
      /* ignore */
    }
  })

  $effect(() => {
    app.start()
    return () => app.stop()
  })

  function toggleTheme() {
    theme = theme === 'dark' ? 'light' : 'dark'
  }

  function onAutoRefreshChange(e: Event) {
    app.setAutoRefresh((e.currentTarget as HTMLInputElement).checked)
  }
</script>

<header class="top">
  <div class="top-inner shell">
    <div class="brand">
      <h1>mount-wrapper</h1>
      <p class="tag">Archive mount dashboard</p>
    </div>
    <nav class="nav" aria-label="Main">
      <button
        type="button"
        class:active={view === 'archives'}
        onclick={() => (view = 'archives')}
      >
        Archives
      </button>
      <button
        type="button"
        class:active={view === 'settings'}
        onclick={() => (view = 'settings')}
      >
        Settings
      </button>
    </nav>
    <div class="actions">
      {#if view === 'archives'}
        <label class="auto">
          <input
            type="checkbox"
            checked={app.autoRefresh}
            onchange={onAutoRefreshChange}
          />
          Auto-refresh
        </label>
      {/if}
      <ConnectionBadge />
      <button type="button" class="secondary" onclick={toggleTheme} title="Toggle light/dark"
        >Theme</button
      >
    </div>
  </div>
</header>

<main class="shell">
  {#if view === 'archives'}
    <Archives />
  {:else}
    <Settings />
  {/if}
</main>

<footer>
  <div class="shell footer-inner">
    <span>
      Bound to localhost only by default ·
      {#if typeof location !== 'undefined'}
        <code>{location.host}</code>
      {/if}
      {#if app.lastRefreshAt}
        · <span>{app.lastRefreshAt}</span>
      {/if}
    </span>
  </div>
</footer>
