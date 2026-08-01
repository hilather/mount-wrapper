package config

import (
	"sort"
)

// HotReloadKeys are public YAML keys that can be applied without process restart.
// Copied from upstream config_io.HOT_RELOAD_KEYS. web_enabled / web_token are
// restart-required: the HTTP server binds and captures token at serve start.
var HotReloadKeys = map[string]struct{}{
	"log_level":                               {},
	"poll_interval_seconds":                   {},
	"reconcile_interval_seconds":              {},
	"name_regex":                              {},
	"recursive":                               {},
	"use_inotify":                             {},
	"stable_file_mode":                        {},
	"min_file_age_seconds":                    {},
	"content_fingerprint":                     {},
	"on_content_change":                       {},
	"max_concurrent_index":                    {},
	"max_concurrent_convert":                  {},
	"max_concurrent_mount":                    {},
	"max_mount_attempts":                      {},
	"max_archive_bytes":                       {},
	"hook_timeout_seconds":                    {},
	"hook_max_retries":                        {},
	"hook_rerun_on_failure":                   {},
	"hooks_parallel":                          {},
	"hooks_stop_on_hard_fail":                 {},
	"hooks_cwd":                               {},
	"cleanup_after":                           {},
	"cleanup_after_seconds":                   {},
	"overlay_cleanup":                         {},
	"quarantine_retain_for":                   {},
	"quarantine_retain_for_seconds":           {},
	"quarantine_max_bytes":                    {},
	"min_free_bytes":                          {},
	"unmount_timeout_seconds":                 {},
	"mount_ready_timeout_seconds":             {},
	"source_dirs":                             {},
	"extra_ratarmount_args":                   {},
	"ratarmount_index_workers":                {},
	"ratarmount_debug":                        {},
	"ratarmount_7z_debug":                     {},
	"ratarmount_log_dir":                      {},
	"ratarmount_rust_log":                     {},
	"convert_7z_nonsolid":                     {},
	"convert_7z_scope":                        {},
	"convert_7z_bin":                          {},
	"convert_7z_cache_dir":                    {},
	"convert_7z_overhead_bytes":               {},
	"convert_7z_flatten_extract_buffer_bytes": {},
	"convert_7z_inner_prefix_strip":           {},
	"convert_7z_flatten_exclude":              {},
	"convert_zip_to_7z":                       {},
	"recursive_mount":                         {},
	"recursive_mount_extensions":              {},
	"index_smallest_first":                    {},
	"archives_dir":                            {},
	"move_archives_to_linux":                  {},
	"archive_relocate_overhead_bytes":         {},
	"write_overlay":                           {},
	"strict_config":                           {},
	"archiveconverter_enabled":                {},
	"archiveconverter_mode":                   {},
	"archiveconverter_backend":                {},
	"archiveconverter_level":                  {},
	"archiveconverter_threads":                {},
	"archiveconverter_verify":                 {},
	"archiveconverter_required":               {},
	"archiveconverter_temp_dir":               {},
	"archiveconverter_native_pipeline":        {},
	"archiveconverter_native_codec":           {},
	"archiveconverter_native_large_threshold": {},
	"archiveconverter_nested_concurrency":     {},
	"archiveconverter_nested_size_budget":     {},
	"archiveconverter_basename_match":         {},
	"archiveconverter_exclude_inner":          {},
	"archiveconverter_exclude_outer":          {},
	"archiveconverter_rename":                 {},
	"archiveconverter_extra_args":             {},
	"archiveconverter_overhead_bytes":         {},
	"archiveconverter_timeout_seconds":        {},
}

// RestartRequiredKeys require a process restart to take effect.
// web_enabled / web_token: HTTP listener and auth close over start-time values.
var RestartRequiredKeys = map[string]struct{}{
	"mount_root":                  {},
	"index_dir":                   {},
	"overlay_dir":                 {},
	"state_db":                    {},
	"hooks_dir":                   {},
	"control_socket":              {},
	"pid_file":                    {},
	"windows_visible":             {},
	"mount_backend":               {},
	"ratarmount_bin":              {},
	"allow_indexes_on_drvfs":      {},
	"archiveconverter_bin":        {},
	"archiveconverter_output_dir": {},
	"web_host":                    {},
	"web_port":                    {},
	"web_enabled":                 {},
	"web_token":                   {},
}

// PublicKeys returns the sorted public YAML/API key names from an empty
// ToPublicMap (parity inventory / SPA settings / config show).
// Dual-form duration aliases (cleanup_after_seconds, …) are accepted on load
// but are not listed here — public snapshot uses human duration strings.
func PublicKeys() []string {
	m := ToPublicMap(&Config{})
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ToPublicMap serializes effective config for API/CLI (YAML-friendly keys).
// Duration fields cleanup_after / quarantine_retain_for are human strings.
func ToPublicMap(cfg *Config) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}
	var threads any
	if cfg.ArchiveconverterThreads != nil {
		threads = *cfg.ArchiveconverterThreads
	}
	var nestedConc any
	if cfg.ArchiveconverterNestedConcurrency != nil {
		nestedConc = *cfg.ArchiveconverterNestedConcurrency
	}

	return map[string]any{
		"version":                                 cfg.Version,
		"source_dirs":                             stringSliceCopy(cfg.SourceDirs),
		"mount_root":                              cfg.MountRoot,
		"index_dir":                               cfg.IndexDir,
		"overlay_dir":                             cfg.OverlayDir,
		"state_db":                                cfg.StateDB,
		"archives_dir":                            cfg.ArchivesDir,
		"move_archives_to_linux":                  cfg.MoveArchivesToLinux,
		"archive_relocate_overhead_bytes":         cfg.ArchiveRelocateOverheadBytes,
		"name_regex":                              cfg.NameRegex,
		"recursive":                               cfg.Recursive,
		"recursive_mount":                         cfg.RecursiveMount,
		"recursive_mount_extensions":              stringSliceCopy(cfg.RecursiveMountExtensions),
		"index_smallest_first":                    cfg.IndexSmallestFirst,
		"poll_interval_seconds":                   num(cfg.PollIntervalSeconds),
		"reconcile_interval_seconds":              num(cfg.ReconcileIntervalSeconds),
		"use_inotify":                             cfg.UseInotify,
		"stable_file_mode":                        cfg.StableFileMode,
		"min_file_age_seconds":                    num(cfg.MinFileAgeSeconds),
		"content_fingerprint":                     cfg.ContentFingerprint,
		"on_content_change":                       cfg.OnContentChange,
		"write_overlay":                           cfg.WriteOverlay,
		"windows_visible":                         cfg.WindowsVisible,
		"allow_indexes_on_drvfs":                  cfg.AllowIndexesOnDrvfs,
		"cleanup_after":                           FormatDuration(cfg.CleanupAfterSeconds),
		"overlay_cleanup":                         cfg.OverlayCleanup,
		"quarantine_retain_for":                   FormatDuration(cfg.QuarantineRetainForSeconds),
		"quarantine_max_bytes":                    cfg.QuarantineMaxBytes,
		"min_free_bytes":                          cfg.MinFreeBytes,
		"max_archive_bytes":                       cfg.MaxArchiveBytes,
		"max_concurrent_index":                    cfg.MaxConcurrentIndex,
		"max_concurrent_convert":                  cfg.MaxConcurrentConvert,
		"max_concurrent_mount":                    cfg.MaxConcurrentMount,
		"max_mount_attempts":                      cfg.MaxMountAttempts,
		"mount_ready_timeout_seconds":             num(cfg.MountReadyTimeoutSeconds),
		"unmount_timeout_seconds":                 num(cfg.UnmountTimeoutSeconds),
		"mount_backend":                           cfg.MountBackend,
		"ratarmount_bin":                          cfg.RatarmountBin,
		"ratarmount_index_workers":                cfg.RatarmountIndexWorkers,
		"ratarmount_debug":                        cfg.RatarmountDebug,
		"ratarmount_7z_debug":                     cfg.Ratarmount7zDebug,
		"ratarmount_log_dir":                      cfg.RatarmountLogDir,
		"ratarmount_rust_log":                     cfg.RatarmountRustLog,
		"convert_7z_nonsolid":                     cfg.Convert7zNonsolid,
		"convert_7z_scope":                        cfg.Convert7zScope,
		"convert_7z_bin":                          cfg.Convert7zBin,
		"convert_7z_cache_dir":                    cfg.Convert7zCacheDir,
		"convert_7z_overhead_bytes":               cfg.Convert7zOverheadBytes,
		"convert_7z_flatten_extract_buffer_bytes": cfg.Convert7zFlattenExtractBuffer,
		"convert_7z_inner_prefix_strip":           cfg.Convert7zInnerPrefixStrip,
		"convert_7z_flatten_exclude":              stringSliceCopy(cfg.Convert7zFlattenExclude),
		"convert_zip_to_7z":                       cfg.ConvertZipTo7z,
		"extra_ratarmount_args":                   stringSliceCopy(cfg.ExtraRatarmountArgs),
		"archiveconverter_enabled":                cfg.ArchiveconverterEnabled,
		"archiveconverter_bin":                    cfg.ArchiveconverterBin,
		"archiveconverter_output_dir":             cfg.ArchiveconverterOutputDir,
		"archiveconverter_mode":                   cfg.ArchiveconverterMode,
		"archiveconverter_backend":                cfg.ArchiveconverterBackend,
		"archiveconverter_level":                  cfg.ArchiveconverterLevel,
		"archiveconverter_threads":                threads,
		"archiveconverter_verify":                 cfg.ArchiveconverterVerify,
		"archiveconverter_required":               cfg.ArchiveconverterRequired,
		"archiveconverter_temp_dir":               cfg.ArchiveconverterTempDir,
		"archiveconverter_native_pipeline":        cfg.ArchiveconverterNativePipeline,
		"archiveconverter_native_codec":           cfg.ArchiveconverterNativeCodec,
		"archiveconverter_native_large_threshold": cfg.ArchiveconverterNativeLargeThreshold,
		"archiveconverter_nested_concurrency":     nestedConc,
		"archiveconverter_nested_size_budget":     cfg.ArchiveconverterNestedSizeBudget,
		"archiveconverter_basename_match":         cfg.ArchiveconverterBasenameMatch,
		"archiveconverter_exclude_inner":          stringSliceCopy(cfg.ArchiveconverterExcludeInner),
		"archiveconverter_exclude_outer":          stringSliceCopy(cfg.ArchiveconverterExcludeOuter),
		"archiveconverter_rename":                 stringSliceCopy(cfg.ArchiveconverterRename),
		"archiveconverter_extra_args":             stringSliceCopy(cfg.ArchiveconverterExtraArgs),
		"archiveconverter_overhead_bytes":         cfg.ArchiveconverterOverheadBytes,
		"archiveconverter_timeout_seconds":        num(cfg.ArchiveconverterTimeoutSeconds),
		"hooks_dir":                               cfg.HooksDir,
		"hooks_parallel":                          cfg.HooksParallel,
		"hooks_stop_on_hard_fail":                 cfg.HooksStopOnHardFail,
		"hook_timeout_seconds":                    num(cfg.HookTimeoutSeconds),
		"hook_max_retries":                        cfg.HookMaxRetries,
		"hook_rerun_on_failure":                   cfg.HookRerunOnFailure,
		"hooks_cwd":                               cfg.HooksCwd,
		"control_socket":                          cfg.ControlSocket,
		"pid_file":                                cfg.PIDFile,
		"web_enabled":                             cfg.WebEnabled,
		"web_host":                                cfg.WebHost,
		"web_port":                                cfg.WebPort,
		"web_token":                               cfg.WebToken,
		"log_level":                               cfg.LogLevel,
		"strict_config":                           cfg.StrictConfig,
	}
}

// Snapshot returns a public config snapshot for API/CLI (config_get shape).
func Snapshot(cfg *Config) map[string]any {
	var path any
	var unknown any
	if cfg != nil {
		if cfg.ConfigPath != "" {
			path = cfg.ConfigPath
		}
		if len(cfg.UnknownKeys) > 0 {
			unknown = append([]string(nil), cfg.UnknownKeys...)
		} else {
			unknown = []string{}
		}
	} else {
		unknown = []string{}
	}
	return map[string]any{
		"config":                ToPublicMap(cfg),
		"config_path":           path,
		"hot_reload_keys":       sortedSetKeys(HotReloadKeys),
		"restart_required_keys": sortedSetKeys(RestartRequiredKeys),
		"unknown_keys":          unknown,
	}
}

// LoadSnapshot loads config from disk (or uses current) and returns a public snapshot.
func LoadSnapshot(path string, current *Config) (map[string]any, error) {
	var cfg *Config
	var err error
	switch {
	case path != "":
		cfg, err = Load(path)
		if err != nil {
			return nil, err
		}
	case current != nil:
		cfg = current
	default:
		return nil, configErrorf("no config path or current config")
	}
	return Snapshot(cfg), nil
}

func num(value float64) any {
	if value == float64(int64(value)) {
		return int(value)
	}
	return value
}

func stringSliceCopy(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func sortedSetKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
