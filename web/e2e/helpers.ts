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

/** Sample archive rows for table / action e2e (mounted + mount_failed). */
export const MOCK_MOUNTED_ARCHIVE = {
  archive_id: 'arc-mounted-1',
  archive_path: '/tmp/archives/demo-mounted.tar.gz',
  archive_basename: 'demo-mounted.tar.gz',
  source_dir: '/tmp/archives',
  status: 'mounted',
  hooks_status: 'done',
  mount_path: '/tmp/mount-wrapper/mounts/arc-mounted-1',
  index_path: '/tmp/mount-wrapper/indexes/arc-mounted-1.sqlite',
  mount_retryable: false,
  size_bytes: 1_048_576,
  metrics: {
    archive_id: 'arc-mounted-1',
    archive_size_bytes: 1_048_576,
    index_size_bytes: 65_536,
    extracted_size_bytes: 4_194_304,
    space_saved_bytes: 4_128_768,
    space_saved_vs_archive_bytes: 3_080_192,
  },
}

export const MOCK_FAILED_ARCHIVE = {
  archive_id: 'arc-failed-1',
  archive_path: '/tmp/archives/demo-failed.7z',
  archive_basename: 'demo-failed.7z',
  source_dir: '/tmp/archives',
  status: 'mount_failed',
  hooks_status: 'none',
  mount_path: null,
  index_path: '/tmp/mount-wrapper/indexes/arc-failed-1.sqlite',
  mount_retryable: true,
  last_error: 'engine exit 1: fuse mount failed',
  size_bytes: 2_097_152,
  metrics: {
    archive_id: 'arc-failed-1',
    archive_size_bytes: 2_097_152,
    index_size_bytes: 32_768,
    extracted_size_bytes: null,
    space_saved_bytes: null,
  },
}

export const MOCK_ARCHIVE_ROWS = [MOCK_MOUNTED_ARCHIVE, MOCK_FAILED_ARCHIVE]

/** Doctor report with named checks for panel assertions. */
export const MOCK_DOCTOR_REPORT = {
  ok: true,
  config_path: '/tmp/e2e/config.yaml',
  notes: [] as string[],
  fixes_applied: [] as string[],
  checks: [
    {
      ok: true,
      severity: 'info',
      name: 'fuse_device',
      message: '/dev/fuse is present and accessible',
      details: {},
    },
    {
      ok: true,
      severity: 'info',
      name: 'ratarmount_rs',
      message: 'ratarmount-rs found on PATH',
      details: {},
    },
    {
      ok: false,
      severity: 'warn',
      name: 'disk_free_index',
      message: 'index_dir free space below warn threshold',
      details: {},
    },
  ],
}

export type ActionPostCall = {
  /** Pathname without host, e.g. `/api/retry`. */
  path: string
  body: Record<string, unknown>
}

export type ShellApiOptions = {
  /** When set, GET /api/archives and /api/status return these rows (default empty). */
  archives?: typeof MOCK_ARCHIVE_ROWS
  counts?: Record<string, number>
  summary?: Record<string, number>
  doctor?: typeof MOCK_DOCTOR_REPORT
  /** Invoked for POST /api/{rescan,unmount,retry,purge} with parsed JSON body. */
  onAction?: (call: ActionPostCall) => void
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}

function parsePostBody(route: Route): Record<string, unknown> {
  try {
    return JSON.parse(route.request().postData() || '{}') as Record<string, unknown>
  } catch {
    return {}
  }
}

function buildArchivesPayload(
  archives: typeof MOCK_ARCHIVE_ROWS,
  counts?: Record<string, number>,
  summary?: Record<string, number>,
) {
  const mounted = archives.filter((a) => a.status === 'mounted').length
  const mountFailed = archives.filter((a) => a.status === 'mount_failed').length
  return {
    archives,
    counts: {
      mounted,
      discovered: archives.length,
      mount_failed: mountFailed,
      indexing: 0,
      mounting: 0,
      converting: 0,
      hooks_running: 0,
      index_failed: 0,
      absent: 0,
      ...counts,
    },
    summary: {
      archive_count: archives.length,
      total_archive_size_bytes: archives.reduce(
        (n, a) => n + (Number(a.metrics?.archive_size_bytes) || a.size_bytes || 0),
        0,
      ),
      total_space_saved_bytes: archives.reduce(
        (n, a) => n + (Number(a.metrics?.space_saved_bytes) || 0),
        0,
      ),
      ...summary,
    },
    version: 'e2e-test',
  }
}

/**
 * Mock control-plane JSON used by the SPA shell on first load (no real daemon).
 * Covers health, status, archives, wsl-info, SSE, doctor, and action POSTs.
 */
export async function mockShellApi(page: Page, opts?: ShellApiOptions) {
  const archives = opts?.archives ?? []
  const payload = buildArchivesPayload(archives, opts?.counts, opts?.summary)
  const doctor = opts?.doctor ?? MOCK_DOCTOR_REPORT

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
      archives: payload.archives,
      counts: payload.counts,
      version: 'e2e-test',
    })
  })

  await page.route('**/api/archives', async (route) => {
    await json(route, payload)
  })

  await page.route('**/api/wsl-info', async (route) => {
    await json(route, {
      mount_root: '/mnt/wsl',
      distro_name: 'e2e-distro',
      unc_mounts: '\\\\wsl.localhost\\e2e-distro\\mnt\\wsl',
      hint: 'e2e mock',
    })
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

  await page.route('**/api/doctor', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.fallback()
      return
    }
    await json(route, doctor)
  })

  // Action POSTs used by Archives toolbar / row actions.
  const actionPaths = ['/api/rescan', '/api/unmount', '/api/retry', '/api/purge'] as const
  for (const path of actionPaths) {
    await page.route(`**${path}`, async (route) => {
      if (route.request().method() !== 'POST') {
        await route.fallback()
        return
      }
      const body = parsePostBody(route)
      opts?.onAction?.({ path, body })

      if (path === '/api/rescan') {
        await json(route, { seen: 2, inserted: 0, stable: 2, assume_stable: !!body.assume_stable })
        return
      }
      if (path === '/api/retry') {
        await json(route, { status: 'queued', archive_id: body.archive_id })
        return
      }
      if (path === '/api/unmount') {
        await json(route, {
          status: 'ok',
          all: !!body.all,
          archive_id: body.archive_id,
        })
        return
      }
      if (path === '/api/purge') {
        // Real API requires yes:true; mirror that so missing confirm body fails the test path.
        if (body.yes !== true) {
          await json(route, { error: 'yes confirmation required', code: 'YES_REQUIRED' }, 400)
          return
        }
        await json(route, {
          status: 'purged',
          archive_id: body.archive_id,
          overlay_action: 'kept',
        })
        return
      }
      await route.fallback()
    })
  }
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

/** Accept the next window.confirm / alert dialog (purge, unmount, rescan assume-stable). */
export function acceptNextDialog(page: Page) {
  page.once('dialog', (dialog) => {
    void dialog.accept()
  })
}
