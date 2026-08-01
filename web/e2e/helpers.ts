import type { Page, Route } from '@playwright/test'
import { SETTINGS_SCHEMA, type SettingsField } from '../src/lib/settings-schema'

function defaultForField(field: SettingsField): unknown {
  switch (field.type) {
    case 'bool':
      return false
    case 'int':
    case 'number':
      return 0
    case 'string_list':
      return []
    case 'select':
      return field.options?.[0] ?? ''
    case 'password':
    case 'text':
    default:
      return ''
  }
}

/** Minimal public config snapshot covering every Settings form key (ints default 0). */
export function buildMockPublicConfig(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const config: Record<string, unknown> = {}
  for (const group of SETTINGS_SCHEMA) {
    for (const field of group.fields) {
      config[field.key] = defaultForField(field)
    }
  }
  // Sensible non-zero / non-empty demo values for fields the tests touch.
  Object.assign(config, {
    source_dirs: ['/tmp/archives'],
    recursive: true,
    name_regex: '.*\\.(tar(\\.gz|\\.bz2|\\.xz)?|zip|7z|tgz)$',
    mount_root: '/tmp/mount-wrapper/mounts',
    index_dir: '/tmp/mount-wrapper/indexes',
    overlay_dir: '/tmp/mount-wrapper/overlays',
    state_db: '/tmp/mount-wrapper/state.db',
    hooks_dir: '/tmp/mount-wrapper/hooks.d',
    control_socket: '/tmp/mount-wrapper/control.sock',
    pid_file: '/tmp/mount-wrapper/mount-wrapper.pid',
    poll_interval_seconds: 30,
    reconcile_interval_seconds: 60,
    stable_file_mode: 'two_scans',
    on_content_change: 'remount_reset_hooks',
    mount_backend: 'rust',
    log_level: 'INFO',
    web_enabled: true,
  })
  Object.assign(config, overrides)
  return config
}

export const MOCK_PUBLIC_CONFIG = buildMockPublicConfig()

export const MOCK_HOT_RELOAD_KEYS = [
  'log_level',
  'poll_interval_seconds',
  'source_dirs',
  'recursive',
  'name_regex',
  'web_enabled',
]

export const MOCK_RESTART_REQUIRED_KEYS = [
  'mount_root',
  'index_dir',
  'overlay_dir',
  'state_db',
  'hooks_dir',
  'control_socket',
  'pid_file',
  'windows_visible',
  'mount_backend',
  'ratarmount_bin',
]

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}

/**
 * Mock control-plane JSON used by the SPA shell on first load (no real daemon).
 * Covers health, status, archives, wsl-info, and SSE events.
 */
export async function mockShellApi(page: Page) {
  await page.route('**/api/health', async (route) => {
    await json(route, {
      ok: true,
      service_reachable: true,
      version: 'e2e-test',
      web_version: 'e2e',
    })
  })

  await page.route('**/api/status', async (route) => {
    await json(route, {
      ok: true,
      archives: [],
      counts: { mounted: 0, discovered: 0 },
      version: 'e2e-test',
    })
  })

  await page.route('**/api/archives', async (route) => {
    await json(route, {
      archives: [],
      counts: { mounted: 0, discovered: 0 },
      summary: {
        archive_count: 0,
        total_archive_size_bytes: 0,
        total_space_saved_bytes: 0,
      },
      version: 'e2e-test',
    })
  })

  await page.route('**/api/wsl-info', async (route) => {
    await json(route, { mount_root: '/mnt/wsl', hint: 'e2e mock' })
  })

  // SSE will error/reconnect with a short body; poll path still drives the UI.
  await page.route('**/api/events**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      headers: {
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      },
      body: 'event: heartbeat\ndata: {}\n\n',
    })
  })
}

export type ConfigPostCall = {
  apply: boolean
  config?: Record<string, unknown>
  patch?: Record<string, unknown>
}

/**
 * Mock GET/POST /api/config for Settings page smoke.
 * Records POST bodies for assertions; dry-run (apply:false) and apply both succeed.
 */
export async function mockConfigApi(
  page: Page,
  opts?: {
    config?: Record<string, unknown>
    onPost?: (call: ConfigPostCall) => void
  },
) {
  const config = buildMockPublicConfig(opts?.config)
  let liveConfig = { ...config }

  await page.route('**/api/config', async (route) => {
    const req = route.request()
    if (req.method() === 'GET') {
      await json(route, {
        config: liveConfig,
        values: liveConfig,
        config_path: '/tmp/e2e/config.yaml',
        hot_reload_keys: MOCK_HOT_RELOAD_KEYS,
        restart_required_keys: MOCK_RESTART_REQUIRED_KEYS,
      })
      return
    }

    if (req.method() === 'POST') {
      let body: ConfigPostCall = { apply: false }
      try {
        body = JSON.parse(req.postData() || '{}') as ConfigPostCall
      } catch {
        /* keep default */
      }
      opts?.onPost?.(body)

      const posted = body.config ?? body.patch ?? {}
      const baseline = { ...liveConfig }
      const next = { ...liveConfig, ...posted }
      const apply = !!body.apply
      if (apply) {
        liveConfig = next
      }

      // Keys that differ from the in-memory baseline (what Validate/Apply report).
      const changedKeys = Object.keys(posted).filter(
        (k) => JSON.stringify(posted[k]) !== JSON.stringify(baseline[k]),
      )

      await json(route, {
        ok: true,
        written: apply,
        reloaded: apply,
        changed_keys: changedKeys,
        hot_reloadable: changedKeys.filter((k) => MOCK_HOT_RELOAD_KEYS.includes(k)),
        restart_required: changedKeys.filter((k) => MOCK_RESTART_REQUIRED_KEYS.includes(k)),
        config: apply ? liveConfig : undefined,
      })
      return
    }

    await route.fallback()
  })
}
