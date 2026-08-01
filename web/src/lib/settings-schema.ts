/**
 * Settings form schema — public config keys grouped for the Settings page
 * (parity with upstream SETTINGS_SCHEMA).
 */

export type FieldType = 'text' | 'password' | 'bool' | 'int' | 'number' | 'select' | 'string_list'

export interface SettingsField {
  key: string
  label: string
  type: FieldType
  help?: string
  /** Field typically requires process restart (also marked from API restart_required_keys). */
  restart?: boolean
  /** Show extra confirm when applying this value. */
  destructive?: boolean
  options?: string[]
}

export interface SettingsGroup {
  id: string
  title: string
  fields: SettingsField[]
}

export const SETTINGS_SCHEMA: SettingsGroup[] = [
  {
    id: 'sources',
    title: 'Sources',
    fields: [
      {
        key: 'source_dirs',
        label: 'source_dirs',
        type: 'string_list',
        help: 'One path per line (Windows D:\\… or /mnt/… / Linux paths)',
      },
      { key: 'recursive', label: 'recursive', type: 'bool', help: 'Walk source directory trees recursively' },
      { key: 'name_regex', label: 'name_regex', type: 'text', help: 'Basename regex for archive discovery' },
    ],
  },
  {
    id: 'paths',
    title: 'Paths',
    fields: [
      { key: 'mount_root', label: 'mount_root', type: 'text', restart: true },
      { key: 'index_dir', label: 'index_dir', type: 'text', restart: true },
      { key: 'overlay_dir', label: 'overlay_dir', type: 'text', restart: true },
      { key: 'state_db', label: 'state_db', type: 'text', restart: true },
      { key: 'hooks_dir', label: 'hooks_dir', type: 'text', restart: true },
      {
        key: 'archives_dir',
        label: 'archives_dir',
        type: 'text',
        help: 'Linux FS directory for relocated archives',
      },
      {
        key: 'move_archives_to_linux',
        label: 'move_archives_to_linux',
        type: 'bool',
        help: 'Move matched archives from source_dirs and remove the original file',
      },
      { key: 'archive_relocate_overhead_bytes', label: 'archive_relocate_overhead_bytes', type: 'int' },
      { key: 'control_socket', label: 'control_socket', type: 'text', restart: true },
      { key: 'pid_file', label: 'pid_file', type: 'text', restart: true },
    ],
  },
  {
    id: 'discovery',
    title: 'Discovery',
    fields: [
      { key: 'poll_interval_seconds', label: 'poll_interval_seconds', type: 'number' },
      { key: 'reconcile_interval_seconds', label: 'reconcile_interval_seconds', type: 'number' },
      { key: 'use_inotify', label: 'use_inotify', type: 'bool' },
      {
        key: 'stable_file_mode',
        label: 'stable_file_mode',
        type: 'select',
        options: ['two_scans', 'min_age', 'both'],
      },
      { key: 'min_file_age_seconds', label: 'min_file_age_seconds', type: 'number' },
      { key: 'content_fingerprint', label: 'content_fingerprint', type: 'bool' },
      {
        key: 'on_content_change',
        label: 'on_content_change',
        type: 'select',
        options: ['remount_reset_hooks', 'remount_keep_hooks'],
      },
      { key: 'max_archive_bytes', label: 'max_archive_bytes', type: 'int', help: '0 = unlimited' },
    ],
  },
  {
    id: 'mount',
    title: 'Mount / ratarmount-rs',
    fields: [
      {
        key: 'recursive_mount',
        label: 'recursive_mount',
        type: 'bool',
        help: 'ratarmount-rs --recursive for nested archives',
      },
      {
        key: 'recursive_mount_extensions',
        label: 'recursive_mount_extensions',
        type: 'string_list',
        help: 'ratarmount-rs --recursive-extensions (one rule per line)',
      },
      {
        key: 'index_smallest_first',
        label: 'index_smallest_first',
        type: 'bool',
        help: 'Index smallest archives before larger ones',
      },
      { key: 'write_overlay', label: 'write_overlay', type: 'bool' },
      {
        key: 'windows_visible',
        label: 'windows_visible',
        type: 'bool',
        restart: true,
        help: 'FUSE allow_other for \\\\wsl.localhost',
      },
      {
        key: 'allow_indexes_on_drvfs',
        label: 'allow_indexes_on_drvfs',
        type: 'bool',
        restart: true,
      },
      {
        key: 'mount_backend',
        label: 'mount_backend',
        type: 'select',
        options: ['rust'],
        restart: true,
        help: 'Only rust (ratarmount-rs) is supported',
      },
      {
        key: 'ratarmount_bin',
        label: 'ratarmount_bin',
        type: 'text',
        restart: true,
        help: 'Path to ratarmount-rs (default: ratarmount-rs on PATH)',
      },
      { key: 'ratarmount_index_workers', label: 'ratarmount_index_workers', type: 'int' },
      { key: 'ratarmount_debug', label: 'ratarmount_debug', type: 'bool' },
      { key: 'ratarmount_7z_debug', label: 'ratarmount_7z_debug', type: 'bool' },
      { key: 'ratarmount_log_dir', label: 'ratarmount_log_dir', type: 'text' },
      { key: 'ratarmount_rust_log', label: 'ratarmount_rust_log', type: 'text' },
      {
        key: 'extra_ratarmount_args',
        label: 'extra_ratarmount_args',
        type: 'string_list',
        help: 'One CLI arg per line',
      },
      {
        key: 'max_concurrent_index',
        label: 'max_concurrent_index',
        type: 'int',
        help: 'Parallel first-time index builds',
      },
      {
        key: 'max_concurrent_convert',
        label: 'max_concurrent_convert',
        type: 'int',
        help: 'Parallel 7z flatten / convert jobs',
      },
      {
        key: 'max_concurrent_mount',
        label: 'max_concurrent_mount',
        type: 'int',
        help: 'Parallel remounts (0 = unlimited)',
      },
      {
        key: 'convert_7z_nonsolid',
        label: 'convert_7z_nonsolid',
        type: 'bool',
        help: 'Flatten solid .7z before mount when needed',
      },
      {
        key: 'convert_7z_scope',
        label: 'convert_7z_scope',
        type: 'select',
        options: ['nested', 'outer', 'flatten', 'all'],
      },
      { key: 'convert_7z_bin', label: 'convert_7z_bin', type: 'text' },
      { key: 'convert_7z_cache_dir', label: 'convert_7z_cache_dir', type: 'text' },
      { key: 'convert_7z_overhead_bytes', label: 'convert_7z_overhead_bytes', type: 'int' },
      {
        key: 'convert_7z_flatten_extract_buffer_bytes',
        label: 'convert_7z_flatten_extract_buffer_bytes',
        type: 'int',
      },
      {
        key: 'convert_7z_inner_prefix_strip',
        label: 'convert_7z_inner_prefix_strip',
        type: 'text',
        help: 'Prefix stripped from nested inner .7z names when flattening (empty = no strip)',
      },
      {
        key: 'convert_7z_flatten_exclude',
        label: 'convert_7z_flatten_exclude',
        type: 'string_list',
        help: 'Glob patterns skipped during 7z flatten',
      },
      {
        key: 'convert_zip_to_7z',
        label: 'convert_zip_to_7z',
        type: 'bool',
        help: 'Repack ZIP archives with embedded archives into stored non-solid 7z before mount',
      },
      {
        key: 'archiveconverter_enabled',
        label: 'archiveconverter_enabled',
        type: 'bool',
        help: 'Prefer external archiveconverter CLI for solid .7z when available',
      },
      { key: 'archiveconverter_bin', label: 'archiveconverter_bin', type: 'text', restart: true },
      {
        key: 'archiveconverter_output_dir',
        label: 'archiveconverter_output_dir',
        type: 'text',
        restart: true,
      },
      {
        key: 'archiveconverter_mode',
        label: 'archiveconverter_mode',
        type: 'select',
        options: ['convert', 'convert-single'],
      },
      {
        key: 'archiveconverter_backend',
        label: 'archiveconverter_backend',
        type: 'select',
        options: ['native', 'cli'],
      },
      { key: 'archiveconverter_level', label: 'archiveconverter_level', type: 'int' },
      { key: 'archiveconverter_threads', label: 'archiveconverter_threads', type: 'int' },
      {
        key: 'archiveconverter_required',
        label: 'archiveconverter_required',
        type: 'bool',
        help: 'Fail index if external convert fails (else fall back)',
      },
      { key: 'archiveconverter_verify', label: 'archiveconverter_verify', type: 'bool' },
      { key: 'archiveconverter_temp_dir', label: 'archiveconverter_temp_dir', type: 'text' },
      { key: 'archiveconverter_native_pipeline', label: 'archiveconverter_native_pipeline', type: 'text' },
      { key: 'archiveconverter_native_codec', label: 'archiveconverter_native_codec', type: 'text' },
      {
        key: 'archiveconverter_native_large_threshold',
        label: 'archiveconverter_native_large_threshold',
        type: 'int',
      },
      {
        key: 'archiveconverter_nested_concurrency',
        label: 'archiveconverter_nested_concurrency',
        type: 'int',
      },
      {
        key: 'archiveconverter_nested_size_budget',
        label: 'archiveconverter_nested_size_budget',
        type: 'int',
      },
      { key: 'archiveconverter_basename_match', label: 'archiveconverter_basename_match', type: 'text' },
      { key: 'archiveconverter_exclude_inner', label: 'archiveconverter_exclude_inner', type: 'string_list' },
      { key: 'archiveconverter_exclude_outer', label: 'archiveconverter_exclude_outer', type: 'string_list' },
      { key: 'archiveconverter_rename', label: 'archiveconverter_rename', type: 'string_list' },
      { key: 'archiveconverter_extra_args', label: 'archiveconverter_extra_args', type: 'string_list' },
      { key: 'archiveconverter_overhead_bytes', label: 'archiveconverter_overhead_bytes', type: 'int' },
      {
        key: 'archiveconverter_timeout_seconds',
        label: 'archiveconverter_timeout_seconds',
        type: 'number',
      },
      { key: 'max_mount_attempts', label: 'max_mount_attempts', type: 'int' },
      { key: 'mount_ready_timeout_seconds', label: 'mount_ready_timeout_seconds', type: 'number' },
      { key: 'unmount_timeout_seconds', label: 'unmount_timeout_seconds', type: 'number' },
    ],
  },
  {
    id: 'hooks',
    title: 'Hooks',
    fields: [
      { key: 'hooks_parallel', label: 'hooks_parallel', type: 'bool' },
      { key: 'hooks_stop_on_hard_fail', label: 'hooks_stop_on_hard_fail', type: 'bool' },
      { key: 'hook_timeout_seconds', label: 'hook_timeout_seconds', type: 'number' },
      { key: 'hook_max_retries', label: 'hook_max_retries', type: 'int' },
      { key: 'hook_rerun_on_failure', label: 'hook_rerun_on_failure', type: 'bool' },
      {
        key: 'hooks_cwd',
        label: 'hooks_cwd',
        type: 'select',
        options: ['mount', 'archive_dir', 'hooks_dir'],
      },
    ],
  },
  {
    id: 'cleanup',
    title: 'Cleanup',
    fields: [
      { key: 'cleanup_after', label: 'cleanup_after', type: 'text', help: 'e.g. 24h, 7d' },
      {
        key: 'overlay_cleanup',
        label: 'overlay_cleanup',
        type: 'select',
        options: ['quarantine', 'delete', 'retain'],
        destructive: true,
      },
      { key: 'quarantine_retain_for', label: 'quarantine_retain_for', type: 'text' },
      { key: 'quarantine_max_bytes', label: 'quarantine_max_bytes', type: 'int', help: '0 = unlimited' },
      { key: 'min_free_bytes', label: 'min_free_bytes', type: 'int' },
    ],
  },
  {
    id: 'web',
    title: 'Web UI',
    fields: [
      {
        key: 'web_enabled',
        label: 'web_enabled',
        type: 'bool',
        restart: true,
        help: 'HTTP API + SPA start at serve time — process restart required',
      },
      {
        key: 'web_host',
        label: 'web_host',
        type: 'text',
        restart: true,
        help: 'Default 127.0.0.1 — restart process after change',
      },
      { key: 'web_port', label: 'web_port', type: 'int', restart: true },
      {
        key: 'web_token',
        label: 'web_token',
        type: 'password',
        restart: true,
        help: 'Optional Bearer token for /api/* (captured at serve start — restart required)',
      },
    ],
  },
  {
    id: 'logging',
    title: 'Logging',
    fields: [
      {
        key: 'log_level',
        label: 'log_level',
        type: 'select',
        options: ['DEBUG', 'INFO', 'WARNING', 'ERROR', 'CRITICAL'],
      },
      { key: 'strict_config', label: 'strict_config', type: 'bool' },
      { key: 'version', label: 'version', type: 'int', help: 'Config schema version (usually 1)' },
    ],
  },
]

export function valueToForm(field: SettingsField, value: unknown): string | boolean {
  if (field.type === 'string_list') {
    if (Array.isArray(value)) return value.map(String).join('\n')
    return value == null ? '' : String(value)
  }
  if (field.type === 'bool') return !!value
  if (value === null || value === undefined) return ''
  return String(value)
}

export function formToValue(field: SettingsField, raw: string | boolean): unknown {
  if (field.type === 'bool') return !!raw
  if (field.type === 'string_list') {
    return String(raw)
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean)
  }
  if (field.type === 'int') {
    const n = parseInt(String(raw), 10)
    if (Number.isNaN(n)) throw new Error(`${field.key}: expected integer`)
    return n
  }
  if (field.type === 'number') {
    const n = Number(raw)
    if (Number.isNaN(n)) throw new Error(`${field.key}: expected number`)
    return n
  }
  return String(raw)
}

/** Collect messages for destructive settings before apply. */
export function destructiveWarnings(config: Record<string, unknown>): string[] {
  const msgs: string[] = []
  if (config.overlay_cleanup === 'delete') {
    msgs.push('overlay_cleanup is set to delete — purged overlays will be permanently removed.')
  }
  if (Array.isArray(config.source_dirs) && config.source_dirs.length === 0) {
    msgs.push('source_dirs is empty — no archives will be discovered.')
  }
  return msgs
}
