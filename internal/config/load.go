package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/hilather/mount-wrapper/internal/paths"
	"gopkg.in/yaml.v3"
)

// knownYAMLKeys is the set of accepted YAML keys (direct + duration dual keys + legacy aliases).
var knownYAMLKeys map[string]struct{}

func init() {
	knownYAMLKeys = make(map[string]struct{}, 128)
	for _, k := range directKeys {
		knownYAMLKeys[k] = struct{}{}
	}
	for k := range durationKeys {
		knownYAMLKeys[k] = struct{}{}
	}
	// Legacy aliases (accepted, mapped onto modern fields).
	for _, k := range []string{"stage_archive_to", "stage_always", "stage_overhead_bytes"} {
		knownYAMLKeys[k] = struct{}{}
	}
}

// YAML keys that map 1:1 onto Config fields (after duration conversion for dual keys).
var directKeys = []string{
	"version",
	"source_dirs",
	"mount_root",
	"index_dir",
	"overlay_dir",
	"state_db",
	"archives_dir",
	"move_archives_to_linux",
	"archive_relocate_overhead_bytes",
	"name_regex",
	"recursive",
	"recursive_mount",
	"recursive_mount_extensions",
	"index_smallest_first",
	"poll_interval_seconds",
	"reconcile_interval_seconds",
	"use_inotify",
	"stable_file_mode",
	"min_file_age_seconds",
	"content_fingerprint",
	"on_content_change",
	"write_overlay",
	"windows_visible",
	"allow_indexes_on_drvfs",
	"overlay_cleanup",
	"quarantine_max_bytes",
	"min_free_bytes",
	"max_archive_bytes",
	"max_concurrent_index",
	"max_concurrent_convert",
	"max_concurrent_mount",
	"max_mount_attempts",
	"mount_ready_timeout_seconds",
	"unmount_timeout_seconds",
	"mount_backend",
	"ratarmount_bin",
	"ratarmount_index_workers",
	"ratarmount_debug",
	"ratarmount_7z_debug",
	"ratarmount_log_dir",
	"ratarmount_rust_log",
	"convert_7z_nonsolid",
	"convert_7z_scope",
	"convert_7z_bin",
	"convert_7z_cache_dir",
	"convert_7z_overhead_bytes",
	"convert_7z_flatten_extract_buffer_bytes",
	"convert_7z_inner_prefix_strip",
	"convert_7z_flatten_exclude",
	"convert_zip_to_7z",
	"extra_ratarmount_args",
	"archiveconverter_enabled",
	"archiveconverter_bin",
	"archiveconverter_output_dir",
	"archiveconverter_mode",
	"archiveconverter_backend",
	"archiveconverter_level",
	"archiveconverter_threads",
	"archiveconverter_verify",
	"archiveconverter_required",
	"archiveconverter_temp_dir",
	"archiveconverter_native_pipeline",
	"archiveconverter_native_codec",
	"archiveconverter_native_large_threshold",
	"archiveconverter_nested_concurrency",
	"archiveconverter_nested_size_budget",
	"archiveconverter_basename_match",
	"archiveconverter_exclude_inner",
	"archiveconverter_exclude_outer",
	"archiveconverter_rename",
	"archiveconverter_extra_args",
	"archiveconverter_overhead_bytes",
	"archiveconverter_timeout_seconds",
	"hooks_dir",
	"hooks_parallel",
	"hooks_stop_on_hard_fail",
	"hook_timeout_seconds",
	"hook_max_retries",
	"hook_rerun_on_failure",
	"hooks_cwd",
	"control_socket",
	"pid_file",
	"web_enabled",
	"web_host",
	"web_port",
	"web_token",
	"log_level",
	"strict_config",
}

// durationKeys maps YAML keys → Config field names stored as seconds.
// Dual human/seconds keys share the same target field.
var durationKeys = map[string]string{
	"cleanup_after":                    "cleanup_after_seconds",
	"quarantine_retain_for":            "quarantine_retain_for_seconds",
	"cleanup_after_seconds":            "cleanup_after_seconds",
	"quarantine_retain_for_seconds":    "quarantine_retain_for_seconds",
	"poll_interval_seconds":            "poll_interval_seconds",
	"reconcile_interval_seconds":       "reconcile_interval_seconds",
	"min_file_age_seconds":             "min_file_age_seconds",
	"hook_timeout_seconds":             "hook_timeout_seconds",
	"unmount_timeout_seconds":          "unmount_timeout_seconds",
	"mount_ready_timeout_seconds":      "mount_ready_timeout_seconds",
	"archiveconverter_timeout_seconds": "archiveconverter_timeout_seconds",
}

// Load reads and validates configuration from a YAML file path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("config file not found: %s", path)
		}
		return nil, configErrorf("cannot read config file %s: %v", path, err)
	}
	return LoadText(string(data), path)
}

// LoadText parses and validates configuration from a YAML string.
// configPath is stored on the resulting Config (may be empty).
func LoadText(text string, configPath string) (*Config, error) {
	var raw any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return nil, configErrorf("invalid YAML: %v", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		// yaml.v3 may decode mapping keys as map[any]any with non-string keys rarely;
		// try conversion.
		if generic, ok := raw.(map[any]any); ok {
			m = make(map[string]any, len(generic))
			for k, v := range generic {
				ks, ok := k.(string)
				if !ok {
					return nil, configErrorf("config root must be a mapping, got non-string key %T", k)
				}
				m[ks] = v
			}
		} else {
			return nil, configErrorf("config root must be a mapping, got %T", raw)
		}
	}
	return FromMap(m, configPath)
}

// FromMap builds a validated Config from a raw mapping (parsed YAML).
func FromMap(raw map[string]any, configPath string) (*Config, error) {
	if raw == nil {
		raw = map[string]any{}
	}

	unknown := make([]string, 0)
	for k := range raw {
		if _, ok := knownYAMLKeys[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)

	strict, err := requireBool(raw, "strict_config", false)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		msg := "unknown config keys: " + strings.Join(unknown, ", ")
		if strict {
			return nil, configErrorf("%s", msg)
		}
		// Warn-mode: track on Config.UnknownKeys (callers may log).
	}

	version := 1
	if v, ok := raw["version"]; ok {
		iv, err := asInt(v, "version")
		if err != nil {
			return nil, err
		}
		version = iv
	}
	if version != 1 {
		return nil, configErrorf("unsupported config version %d; this package supports version 1 only", version)
	}

	sourceDirs, err := requireNonEmptyStringList(raw, "source_dirs", true)
	if err != nil {
		return nil, err
	}

	nameRegex, err := requireStr(raw, "name_regex", DefaultNameRegex)
	if err != nil {
		return nil, err
	}
	if _, err := regexp.Compile(nameRegex); err != nil {
		return nil, configErrorf("name_regex: invalid regular expression: %v", err)
	}

	stableFileMode, err := requireStr(raw, "stable_file_mode", StableFileTwoScans)
	if err != nil {
		return nil, err
	}
	if _, ok := StableFileModes[stableFileMode]; !ok {
		return nil, configErrorf("stable_file_mode: must be one of %s, got %q", sortedKeys(StableFileModes), stableFileMode)
	}

	overlayCleanup, err := requireStr(raw, "overlay_cleanup", OverlayCleanupQuarantine)
	if err != nil {
		return nil, err
	}
	if _, ok := OverlayCleanupModes[overlayCleanup]; !ok {
		return nil, configErrorf("overlay_cleanup: must be one of %s, got %q", sortedKeys(OverlayCleanupModes), overlayCleanup)
	}

	onContentChange, err := requireStr(raw, "on_content_change", OnContentRemountResetHooks)
	if err != nil {
		return nil, err
	}
	if _, ok := OnContentChangeModes[onContentChange]; !ok {
		return nil, configErrorf("on_content_change: must be one of %s, got %q", sortedKeys(OnContentChangeModes), onContentChange)
	}

	hooksCwd, err := requireStr(raw, "hooks_cwd", HooksCwdMount)
	if err != nil {
		return nil, err
	}
	if _, ok := HooksCwdModes[hooksCwd]; !ok {
		return nil, configErrorf("hooks_cwd: must be one of %s, got %q", sortedKeys(HooksCwdModes), hooksCwd)
	}

	logLevel, err := requireStr(raw, "log_level", "INFO")
	if err != nil {
		return nil, err
	}
	logLevel = strings.ToUpper(logLevel)
	if _, ok := LogLevels[logLevel]; !ok {
		return nil, configErrorf("log_level: must be one of %s, got %q", sortedKeys(LogLevels), logLevel)
	}

	extraArgs, err := requireStringList(raw, "extra_ratarmount_args")
	if err != nil {
		return nil, err
	}

	acExcludeInner, err := requireStringList(raw, "archiveconverter_exclude_inner")
	if err != nil {
		return nil, err
	}
	acExcludeOuter, err := requireStringList(raw, "archiveconverter_exclude_outer")
	if err != nil {
		return nil, err
	}
	acRename, err := requireStringList(raw, "archiveconverter_rename")
	if err != nil {
		return nil, err
	}
	acExtra, err := requireStringList(raw, "archiveconverter_extra_args")
	if err != nil {
		return nil, err
	}

	acMode, err := requireStr(raw, "archiveconverter_mode", ArchiveconverterModeConvert)
	if err != nil {
		return nil, err
	}
	acMode = strings.ToLower(strings.TrimSpace(acMode))
	if _, ok := ArchiveconverterModes[acMode]; !ok {
		return nil, configErrorf("archiveconverter_mode: must be one of %s, got %q", sortedKeys(ArchiveconverterModes), acMode)
	}

	acBackend, err := requireStr(raw, "archiveconverter_backend", ArchiveconverterBackendNative)
	if err != nil {
		return nil, err
	}
	acBackend = strings.ToLower(strings.TrimSpace(acBackend))
	if _, ok := ArchiveconverterBackends[acBackend]; !ok {
		return nil, configErrorf("archiveconverter_backend: must be one of %s, got %q", sortedKeys(ArchiveconverterBackends), acBackend)
	}

	acLevel, err := requireInt(raw, "archiveconverter_level", 5, intPtr(0))
	if err != nil {
		return nil, err
	}
	if acLevel > 9 {
		return nil, configErrorf("archiveconverter_level: must be <= 9, got %d", acLevel)
	}

	var acThreads *int
	if v, ok := raw["archiveconverter_threads"]; ok && v != nil {
		t, err := requireInt(raw, "archiveconverter_threads", 0, intPtr(0))
		if err != nil {
			return nil, err
		}
		if t != 0 {
			acThreads = &t
		}
	}

	acTimeout, err := requireFloatSeconds(raw, "archiveconverter_timeout_seconds", 0, 0)
	if err != nil {
		return nil, err
	}
	acOverhead, err := requireInt(raw, "archiveconverter_overhead_bytes", 64*1024*1024, intPtr(0))
	if err != nil {
		return nil, err
	}
	acNativeLarge, err := requireInt(raw, "archiveconverter_native_large_threshold", 0, intPtr(0))
	if err != nil {
		return nil, err
	}

	acNestedBudget := ""
	if v, ok := raw["archiveconverter_nested_size_budget"]; ok && v != nil {
		acNestedBudget = strings.TrimSpace(fmt.Sprint(v))
		if acNestedBudget == "<nil>" {
			acNestedBudget = ""
		}
	}

	var acNestedConcurrency *int
	if v, ok := raw["archiveconverter_nested_concurrency"]; ok && v != nil {
		n, err := requireInt(raw, "archiveconverter_nested_concurrency", 0, intPtr(0))
		if err != nil {
			return nil, err
		}
		acNestedConcurrency = &n
	}

	// recursive_mount_extensions
	extDefault := append([]string(nil), DefaultRecursiveMountExtensions...)
	extRaw, hasExt := raw["recursive_mount_extensions"]
	if !hasExt || extRaw == nil {
		extRaw = extDefault
	}
	extList, err := asStringList(extRaw, "recursive_mount_extensions", false)
	if err != nil {
		return nil, err
	}

	// Duration fields: human keys preferred; *_seconds accepted for tests.
	var cleanupAfter float64
	switch {
	case hasKey(raw, "cleanup_after"):
		cleanupAfter, err = ParseDuration(raw["cleanup_after"], "cleanup_after")
	case hasKey(raw, "cleanup_after_seconds"):
		cleanupAfter, err = ParseDuration(raw["cleanup_after_seconds"], "cleanup_after_seconds")
	default:
		cleanupAfter = 24 * 3600
	}
	if err != nil {
		return nil, err
	}

	var quarantineRetain float64
	switch {
	case hasKey(raw, "quarantine_retain_for"):
		quarantineRetain, err = ParseDuration(raw["quarantine_retain_for"], "quarantine_retain_for")
	case hasKey(raw, "quarantine_retain_for_seconds"):
		quarantineRetain, err = ParseDuration(raw["quarantine_retain_for_seconds"], "quarantine_retain_for_seconds")
	default:
		quarantineRetain = 168 * 3600
	}
	if err != nil {
		return nil, err
	}

	pollInterval, err := requireFloatSeconds(raw, "poll_interval_seconds", 60, 1)
	if err != nil {
		return nil, err
	}
	reconcileInterval, err := requireFloatSeconds(raw, "reconcile_interval_seconds", 30, 1)
	if err != nil {
		return nil, err
	}
	minFileAge, err := requireFloatSeconds(raw, "min_file_age_seconds", 30, 0)
	if err != nil {
		return nil, err
	}
	hookTimeout, err := requireFloatSeconds(raw, "hook_timeout_seconds", 3600, 1)
	if err != nil {
		return nil, err
	}
	unmountTimeout, err := requireFloatSeconds(raw, "unmount_timeout_seconds", 60, 1)
	if err != nil {
		return nil, err
	}
	mountReadyTimeout, err := requireFloatSeconds(raw, "mount_ready_timeout_seconds", 86400, 1)
	if err != nil {
		return nil, err
	}

	maxConcurrentIndex, err := requireInt(raw, "max_concurrent_index", 1, intPtr(1))
	if err != nil {
		return nil, err
	}
	maxConcurrentConvert, err := requireInt(raw, "max_concurrent_convert", 1, intPtr(1))
	if err != nil {
		return nil, err
	}
	maxConcurrentMount, err := requireInt(raw, "max_concurrent_mount", 0, intPtr(0))
	if err != nil {
		return nil, err
	}
	maxMountAttempts, err := requireInt(raw, "max_mount_attempts", 10, intPtr(1))
	if err != nil {
		return nil, err
	}
	ratarmountIndexWorkers, err := requireInt(raw, "ratarmount_index_workers", 0, intPtr(0))
	if err != nil {
		return nil, err
	}
	ratarmountDebug, err := requireInt(raw, "ratarmount_debug", 0, intPtr(0))
	if err != nil {
		return nil, err
	}
	if ratarmountDebug > 3 {
		return nil, configErrorf("ratarmount_debug: must be <= 3, got %d", ratarmountDebug)
	}
	ratarmount7zDebug, err := requireBool(raw, "ratarmount_7z_debug", false)
	if err != nil {
		return nil, err
	}
	ratarmountLogDir := strings.TrimSpace(stringOrEmpty(raw["ratarmount_log_dir"]))
	ratarmountRustLog := strings.TrimSpace(stringOrEmpty(raw["ratarmount_rust_log"]))

	convert7zNonsolid, err := requireBool(raw, "convert_7z_nonsolid", false)
	if err != nil {
		return nil, err
	}
	convert7zScope, err := requireStr(raw, "convert_7z_scope", Convert7zScopeNested)
	if err != nil {
		return nil, err
	}
	convert7zScope = strings.ToLower(strings.TrimSpace(convert7zScope))
	if _, ok := Convert7zScopes[convert7zScope]; !ok {
		return nil, configErrorf("convert_7z_scope: expected nested|outer|flatten|all, got %q", convert7zScope)
	}
	convert7zBin, err := requireStr(raw, "convert_7z_bin", "7z")
	if err != nil {
		return nil, err
	}
	convert7zBin = strings.TrimSpace(convert7zBin)
	if convert7zBin == "" {
		convert7zBin = "7z"
	}
	convert7zCacheDir := strings.TrimSpace(stringOrEmpty(raw["convert_7z_cache_dir"]))
	convert7zOverhead, err := requireInt(raw, "convert_7z_overhead_bytes", 64*1024*1024, intPtr(0))
	if err != nil {
		return nil, err
	}
	convert7zFlattenBuf, err := requireInt(raw, "convert_7z_flatten_extract_buffer_bytes", 10*1024*1024*1024, intPtr(0))
	if err != nil {
		return nil, err
	}
	convert7zInnerPrefix, err := requireStr(raw, "convert_7z_inner_prefix_strip", "")
	if err != nil {
		return nil, err
	}
	convert7zInnerPrefix = strings.TrimSpace(convert7zInnerPrefix)

	convert7zExclude, err := requireNonEmptyStringList(raw, "convert_7z_flatten_exclude", false)
	if err != nil {
		return nil, err
	}
	convertZipTo7z, err := requireBool(raw, "convert_zip_to_7z", true)
	if err != nil {
		return nil, err
	}

	hookMaxRetries, err := requireInt(raw, "hook_max_retries", 3, intPtr(0))
	if err != nil {
		return nil, err
	}
	quarantineMaxBytes, err := requireInt(raw, "quarantine_max_bytes", 0, intPtr(0))
	if err != nil {
		return nil, err
	}
	minFreeBytes, err := requireInt(raw, "min_free_bytes", 2*1024*1024*1024, intPtr(0))
	if err != nil {
		return nil, err
	}
	maxArchiveBytes, err := requireInt(raw, "max_archive_bytes", 0, intPtr(0))
	if err != nil {
		return nil, err
	}

	// Legacy stage_overhead_bytes → archive_relocate_overhead_bytes
	stageOverheadDefault := 64 * 1024 * 1024
	if hasKey(raw, "stage_overhead_bytes") {
		stageOverheadDefault, err = requireInt(raw, "stage_overhead_bytes", 64*1024*1024, intPtr(0))
		if err != nil {
			return nil, err
		}
	}
	archiveRelocateOverhead := stageOverheadDefault
	if hasKey(raw, "archive_relocate_overhead_bytes") {
		archiveRelocateOverhead, err = requireInt(raw, "archive_relocate_overhead_bytes", stageOverheadDefault, intPtr(0))
		if err != nil {
			return nil, err
		}
	}

	mountBackendRaw, err := requireStr(raw, "mount_backend", BackendRust)
	if err != nil {
		return nil, err
	}
	mountBackend, err := NormalizeMountBackend(mountBackendRaw)
	if err != nil {
		return nil, err
	}

	pathKeys := []string{
		"mount_root", "index_dir", "overlay_dir", "state_db",
		"hooks_dir", "control_socket", "pid_file",
	}
	pathFields := make(map[string]string, len(pathKeys))
	for _, k := range pathKeys {
		p, err := pathStr(raw, k, DefaultPaths[k])
		if err != nil {
			return nil, err
		}
		if p == "" {
			return nil, configErrorf("%s: must be a non-empty path", k)
		}
		pathFields[k] = p
	}

	var ratarmountBin string
	if hasKey(raw, "ratarmount_bin") {
		ratarmountBin, err = pathStr(raw, "ratarmount_bin", DefaultRatarmountBin(mountBackend))
		if err != nil {
			return nil, err
		}
		if ratarmountBin == "" {
			return nil, configErrorf("ratarmount_bin: must be a non-empty path when set")
		}
	} else {
		ratarmountBin = DefaultRatarmountBin(mountBackend)
	}

	acBinDefault := DefaultArchiveconverterBin()
	acBin, err := pathStr(raw, "archiveconverter_bin", acBinDefault)
	if err != nil {
		return nil, err
	}
	acOutputDir, err := pathStr(raw, "archiveconverter_output_dir", DefaultPaths["archiveconverter_output_dir"])
	if err != nil {
		return nil, err
	}
	if acOutputDir == "" {
		return nil, configErrorf("archiveconverter_output_dir: must be a non-empty path")
	}
	acTempDir := strings.TrimSpace(stringOrEmpty(raw["archiveconverter_temp_dir"]))

	// archives_dir with legacy stage_archive_to
	archivesDir := strings.TrimSpace(stringOrEmpty(raw["archives_dir"]))
	if archivesDir == "" {
		archivesDir = strings.TrimSpace(stringOrEmpty(raw["stage_archive_to"]))
	}

	var moveArchives bool
	if hasKey(raw, "move_archives_to_linux") {
		moveArchives, err = requireBool(raw, "move_archives_to_linux", false)
	} else {
		moveArchives, err = requireBool(raw, "stage_always", false)
	}
	if err != nil {
		return nil, err
	}

	allowDrvfs, err := requireBool(raw, "allow_indexes_on_drvfs", false)
	if err != nil {
		return nil, err
	}
	if !allowDrvfs {
		for _, key := range []string{"mount_root", "index_dir", "overlay_dir", "state_db"} {
			if paths.IsDrvFsPath(pathFields[key]) {
				return nil, configErrorf(
					"%s=%q appears to be on DrvFs (/mnt/<drive>). "+
						"Keep indexes/overlays/mounts on the Linux filesystem, or set allow_indexes_on_drvfs: true",
					key, pathFields[key],
				)
			}
		}
		if archivesDir != "" && paths.IsDrvFsPath(archivesDir) {
			return nil, configErrorf(
				"archives_dir=%q appears to be on DrvFs. Use a Linux filesystem path, or set allow_indexes_on_drvfs: true",
				archivesDir,
			)
		}
		if paths.IsDrvFsPath(acOutputDir) {
			return nil, configErrorf(
				"archiveconverter_output_dir=%q appears to be on DrvFs. Use a Linux filesystem path, or set allow_indexes_on_drvfs: true",
				acOutputDir,
			)
		}
	}

	// Booleans and remaining fields
	recursive, err := requireBool(raw, "recursive", false)
	if err != nil {
		return nil, err
	}
	recursiveMount, err := requireBool(raw, "recursive_mount", true)
	if err != nil {
		return nil, err
	}
	indexSmallestFirst, err := requireBool(raw, "index_smallest_first", true)
	if err != nil {
		return nil, err
	}
	useInotify, err := requireBool(raw, "use_inotify", true)
	if err != nil {
		return nil, err
	}
	contentFingerprint, err := requireBool(raw, "content_fingerprint", true)
	if err != nil {
		return nil, err
	}
	writeOverlay, err := requireBool(raw, "write_overlay", true)
	if err != nil {
		return nil, err
	}
	windowsVisible, err := requireBool(raw, "windows_visible", true)
	if err != nil {
		return nil, err
	}
	acEnabled, err := requireBool(raw, "archiveconverter_enabled", false)
	if err != nil {
		return nil, err
	}
	acVerify, err := requireBool(raw, "archiveconverter_verify", false)
	if err != nil {
		return nil, err
	}
	acRequired, err := requireBool(raw, "archiveconverter_required", false)
	if err != nil {
		return nil, err
	}
	acNativePipeline, err := requireStr(raw, "archiveconverter_native_pipeline", "parallel")
	if err != nil {
		return nil, err
	}
	acNativeCodec, err := requireStr(raw, "archiveconverter_native_codec", "liblzma")
	if err != nil {
		return nil, err
	}
	acBasenameMatch, err := requireBool(raw, "archiveconverter_basename_match", false)
	if err != nil {
		return nil, err
	}
	hooksParallel, err := requireBool(raw, "hooks_parallel", false)
	if err != nil {
		return nil, err
	}
	hooksStopOnHardFail, err := requireBool(raw, "hooks_stop_on_hard_fail", true)
	if err != nil {
		return nil, err
	}
	hookRerunOnFailure, err := requireBool(raw, "hook_rerun_on_failure", false)
	if err != nil {
		return nil, err
	}
	webEnabled, err := requireBool(raw, "web_enabled", false)
	if err != nil {
		return nil, err
	}
	webHost, err := requireStr(raw, "web_host", DefaultWebHost)
	if err != nil {
		return nil, err
	}
	webPort, err := requireWebPort(raw)
	if err != nil {
		return nil, err
	}
	webToken, err := requireStr(raw, "web_token", "")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Version:                              version,
		SourceDirs:                           sourceDirs,
		MountRoot:                            pathFields["mount_root"],
		IndexDir:                             pathFields["index_dir"],
		OverlayDir:                           pathFields["overlay_dir"],
		StateDB:                              pathFields["state_db"],
		ArchivesDir:                          archivesDir,
		MoveArchivesToLinux:                  moveArchives,
		ArchiveRelocateOverheadBytes:         archiveRelocateOverhead,
		NameRegex:                            nameRegex,
		Recursive:                            recursive,
		RecursiveMount:                       recursiveMount,
		RecursiveMountExtensions:             extList,
		IndexSmallestFirst:                   indexSmallestFirst,
		PollIntervalSeconds:                  pollInterval,
		ReconcileIntervalSeconds:             reconcileInterval,
		UseInotify:                           useInotify,
		StableFileMode:                       stableFileMode,
		MinFileAgeSeconds:                    minFileAge,
		ContentFingerprint:                   contentFingerprint,
		OnContentChange:                      onContentChange,
		WriteOverlay:                         writeOverlay,
		WindowsVisible:                       windowsVisible,
		AllowIndexesOnDrvfs:                  allowDrvfs,
		CleanupAfterSeconds:                  cleanupAfter,
		OverlayCleanup:                       overlayCleanup,
		QuarantineRetainForSeconds:           quarantineRetain,
		QuarantineMaxBytes:                   quarantineMaxBytes,
		MinFreeBytes:                         minFreeBytes,
		MaxArchiveBytes:                      maxArchiveBytes,
		MaxConcurrentIndex:                   maxConcurrentIndex,
		MaxConcurrentConvert:                 maxConcurrentConvert,
		MaxConcurrentMount:                   maxConcurrentMount,
		MaxMountAttempts:                     maxMountAttempts,
		MountReadyTimeoutSeconds:             mountReadyTimeout,
		UnmountTimeoutSeconds:                unmountTimeout,
		MountBackend:                         mountBackend,
		RatarmountBin:                        ratarmountBin,
		RatarmountIndexWorkers:               ratarmountIndexWorkers,
		RatarmountDebug:                      ratarmountDebug,
		Ratarmount7zDebug:                    ratarmount7zDebug,
		RatarmountLogDir:                     ratarmountLogDir,
		RatarmountRustLog:                    ratarmountRustLog,
		Convert7zNonsolid:                    convert7zNonsolid,
		Convert7zScope:                       convert7zScope,
		Convert7zBin:                         convert7zBin,
		Convert7zCacheDir:                    convert7zCacheDir,
		Convert7zOverheadBytes:               convert7zOverhead,
		Convert7zFlattenExtractBuffer:        convert7zFlattenBuf,
		Convert7zInnerPrefixStrip:            convert7zInnerPrefix,
		Convert7zFlattenExclude:              convert7zExclude,
		ConvertZipTo7z:                       convertZipTo7z,
		ExtraRatarmountArgs:                  extraArgs,
		ArchiveconverterEnabled:              acEnabled,
		ArchiveconverterBin:                  acBin,
		ArchiveconverterOutputDir:            acOutputDir,
		ArchiveconverterMode:                 acMode,
		ArchiveconverterBackend:              acBackend,
		ArchiveconverterLevel:                acLevel,
		ArchiveconverterThreads:              acThreads,
		ArchiveconverterVerify:               acVerify,
		ArchiveconverterRequired:             acRequired,
		ArchiveconverterTempDir:              acTempDir,
		ArchiveconverterNativePipeline:       acNativePipeline,
		ArchiveconverterNativeCodec:          acNativeCodec,
		ArchiveconverterNativeLargeThreshold: acNativeLarge,
		ArchiveconverterNestedConcurrency:    acNestedConcurrency,
		ArchiveconverterNestedSizeBudget:     acNestedBudget,
		ArchiveconverterBasenameMatch:        acBasenameMatch,
		ArchiveconverterExcludeInner:         acExcludeInner,
		ArchiveconverterExcludeOuter:         acExcludeOuter,
		ArchiveconverterRename:               acRename,
		ArchiveconverterExtraArgs:            acExtra,
		ArchiveconverterOverheadBytes:        acOverhead,
		ArchiveconverterTimeoutSeconds:       acTimeout,
		HooksDir:                             pathFields["hooks_dir"],
		HooksParallel:                        hooksParallel,
		HooksStopOnHardFail:                  hooksStopOnHardFail,
		HookTimeoutSeconds:                   hookTimeout,
		HookMaxRetries:                       hookMaxRetries,
		HookRerunOnFailure:                   hookRerunOnFailure,
		HooksCwd:                             hooksCwd,
		ControlSocket:                        pathFields["control_socket"],
		PIDFile:                              pathFields["pid_file"],
		WebEnabled:                           webEnabled,
		WebHost:                              webHost,
		WebPort:                              webPort,
		WebToken:                             webToken,
		LogLevel:                             logLevel,
		StrictConfig:                         strict,
		ConfigPath:                           configPath,
		UnknownKeys:                          unknown,
	}
	return cfg, nil
}

// --- helpers ---

func hasKey(raw map[string]any, key string) bool {
	_, ok := raw[key]
	return ok
}

func intPtr(v int) *int { return &v }

func sortedKeys(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "[" + strings.Join(keys, " ") + "]"
}

func stringOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func requireBool(raw map[string]any, key string, def bool) (bool, error) {
	if !hasKey(raw, key) {
		return def, nil
	}
	v := raw[key]
	b, ok := v.(bool)
	if !ok {
		return false, configErrorf("%s: expected bool, got %T", key, v)
	}
	return b, nil
}

func requireStr(raw map[string]any, key, def string) (string, error) {
	if !hasKey(raw, key) {
		return def, nil
	}
	v := raw[key]
	s, ok := v.(string)
	if !ok {
		return "", configErrorf("%s: expected string, got %T", key, v)
	}
	return s, nil
}

func pathStr(raw map[string]any, key, def string) (string, error) {
	return requireStr(raw, key, def)
}

func asInt(v any, key string) (int, error) {
	switch n := v.(type) {
	case bool:
		return 0, configErrorf("%s: expected integer, got bool", key)
	case int:
		return n, nil
	case int8:
		return int(n), nil
	case int16:
		return int(n), nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case uint:
		return int(n), nil
	case uint8:
		return int(n), nil
	case uint16:
		return int(n), nil
	case uint32:
		return int(n), nil
	case uint64:
		return int(n), nil
	default:
		return 0, configErrorf("%s: expected integer, got %T", key, v)
	}
}

func requireInt(raw map[string]any, key string, def int, minValue *int) (int, error) {
	if !hasKey(raw, key) {
		return def, nil
	}
	n, err := asInt(raw[key], key)
	if err != nil {
		return 0, err
	}
	if minValue != nil && n < *minValue {
		return 0, configErrorf("%s: must be >= %d, got %d", key, *minValue, n)
	}
	return n, nil
}

func requireWebPort(raw map[string]any) (int, error) {
	port, err := requireInt(raw, "web_port", DefaultWebPort, intPtr(1))
	if err != nil {
		return 0, err
	}
	if port > 65535 {
		return 0, configErrorf("web_port: must be <= 65535, got %d", port)
	}
	return port, nil
}

func requireFloatSeconds(raw map[string]any, key string, def, minValue float64) (float64, error) {
	if !hasKey(raw, key) {
		return def, nil
	}
	seconds, err := ParseDuration(raw[key], key)
	if err != nil {
		return 0, err
	}
	if seconds < minValue {
		return 0, configErrorf("%s: must be >= %v, got %v", key, minValue, seconds)
	}
	return seconds, nil
}

func requireStringList(raw map[string]any, key string) ([]string, error) {
	if !hasKey(raw, key) || raw[key] == nil {
		return []string{}, nil
	}
	return asStringList(raw[key], key, false)
}

// requireNonEmptyStringList parses a string list; when requireNonEmptyItems is true
// each item must be non-empty after trim (source_dirs). When allowMissing is true,
// missing key yields empty list.
func requireNonEmptyStringList(raw map[string]any, key string, nonEmptyItems bool) ([]string, error) {
	if !hasKey(raw, key) || raw[key] == nil {
		return []string{}, nil
	}
	return asStringList(raw[key], key, nonEmptyItems)
}

func asStringList(v any, key string, nonEmptyItems bool) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		// yaml may produce []string if typed, but Unmarshal to any uses []any
		if ss, ok := v.([]string); ok {
			out := make([]string, 0, len(ss))
			for i, item := range ss {
				if nonEmptyItems {
					item = strings.TrimSpace(item)
					if item == "" {
						return nil, configErrorf("%s[%d]: expected non-empty string", key, i)
					}
				}
				out = append(out, item)
			}
			return out, nil
		}
		return nil, configErrorf("%s: expected list of strings", key)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, configErrorf("%s[%d]: expected string", key, i)
		}
		if nonEmptyItems {
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, configErrorf("%s[%d]: expected non-empty string", key, i)
			}
		}
		out = append(out, s)
	}
	return out, nil
}
