package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFromMap_emptyDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 {
		t.Fatalf("version=%d", cfg.Version)
	}
	if len(cfg.SourceDirs) != 0 {
		t.Fatalf("source_dirs=%v", cfg.SourceDirs)
	}
	if cfg.OverlayCleanup != OverlayCleanupQuarantine {
		t.Fatalf("overlay_cleanup=%s", cfg.OverlayCleanup)
	}
	if cfg.StableFileMode != StableFileTwoScans {
		t.Fatalf("stable_file_mode=%s", cfg.StableFileMode)
	}
	if cfg.PollIntervalSeconds != 60 {
		t.Fatalf("poll=%v", cfg.PollIntervalSeconds)
	}
	if cfg.CleanupAfterSeconds != 24*3600 {
		t.Fatalf("cleanup=%v", cfg.CleanupAfterSeconds)
	}
	if cfg.QuarantineRetainForSeconds != 168*3600 {
		t.Fatalf("quarantine=%v", cfg.QuarantineRetainForSeconds)
	}
	if cfg.MaxConcurrentIndex != 1 || cfg.MaxConcurrentConvert != 1 || cfg.MaxConcurrentMount != 0 {
		t.Fatalf("concurrency index=%d convert=%d mount=%d",
			cfg.MaxConcurrentIndex, cfg.MaxConcurrentConvert, cfg.MaxConcurrentMount)
	}
	if !cfg.WindowsVisible || !cfg.WriteOverlay {
		t.Fatal("windows_visible/write_overlay defaults")
	}
	if cfg.NameRegex != DefaultNameRegex {
		t.Fatalf("name_regex default mismatch")
	}
	if cfg.ControlSocket != "/run/mount-wrapper/control.sock" {
		t.Fatalf("control_socket=%s", cfg.ControlSocket)
	}
	if cfg.PIDFile != "/run/mount-wrapper/mount-wrapper.pid" {
		t.Fatalf("pid_file=%s", cfg.PIDFile)
	}
	if cfg.MountRoot != "/var/lib/mount-wrapper/mounts" {
		t.Fatalf("mount_root=%s", cfg.MountRoot)
	}
	if cfg.ArchiveconverterOutputDir != "/var/lib/mount-wrapper/converted" {
		t.Fatalf("converted=%s", cfg.ArchiveconverterOutputDir)
	}
	if cfg.WebEnabled {
		t.Fatal("web_enabled should default false")
	}
	if cfg.MountBackend != BackendRust {
		t.Fatalf("mount_backend=%s", cfg.MountBackend)
	}
	if cfg.RatarmountBin != DefaultRustRatarmountBin {
		t.Fatalf("ratarmount_bin=%s want %s", cfg.RatarmountBin, DefaultRustRatarmountBin)
	}
	re, err := cfg.CompiledNameRegex()
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("foo.tar.gz") || !re.MatchString("bar.zip") || re.MatchString("bar.rar") {
		t.Fatal("name_regex match unexpected")
	}
}

func TestLoadText_minimalYAML(t *testing.T) {
	t.Parallel()
	text := `
version: 1
source_dirs:
  - /var/lib/mount-wrapper/inbox
poll_interval_seconds: 30
log_level: DEBUG
`
	cfg, err := LoadText(text, "/tmp/test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SourceDirs) != 1 || cfg.SourceDirs[0] != "/var/lib/mount-wrapper/inbox" {
		t.Fatalf("source_dirs=%v", cfg.SourceDirs)
	}
	if cfg.PollIntervalSeconds != 30 {
		t.Fatalf("poll=%v", cfg.PollIntervalSeconds)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Fatalf("log_level=%s", cfg.LogLevel)
	}
	if cfg.ConfigPath != "/tmp/test.yaml" {
		t.Fatalf("config_path=%s", cfg.ConfigPath)
	}
}

func TestLoadText_emptyDocument(t *testing.T) {
	t.Parallel()
	cfg, err := LoadText("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 {
		t.Fatalf("version=%d", cfg.Version)
	}
}

func TestLoad_missingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoad_invalidYAML(t *testing.T) {
	t.Parallel()
	_, err := LoadText(": : :", "")
	if err == nil || !strings.Contains(err.Error(), "invalid YAML") {
		t.Fatalf("err=%v", err)
	}
}

func TestFromMap_unsupportedVersion(t *testing.T) {
	t.Parallel()
	_, err := FromMap(map[string]any{"version": 2}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("err=%v", err)
	}
}

func TestFromMap_invalidRegex(t *testing.T) {
	t.Parallel()
	_, err := FromMap(map[string]any{"name_regex": "[unclosed"}, "")
	if err == nil || !strings.Contains(err.Error(), "name_regex") {
		t.Fatalf("err=%v", err)
	}
}

func TestFromMap_badEnums(t *testing.T) {
	t.Parallel()
	cases := []map[string]any{
		{"stable_file_mode": "eventually"},
		{"overlay_cleanup": "shred"},
		{"on_content_change": "ignore"},
		{"hooks_cwd": "home"},
		{"log_level": "VERBOSE"},
		{"convert_7z_scope": "maybe"},
		{"archiveconverter_mode": "fast"},
		{"archiveconverter_backend": "wasm"},
	}
	for _, raw := range cases {
		if _, err := FromMap(raw, ""); err == nil {
			t.Fatalf("expected error for %v", raw)
		}
	}
}

func TestFromMap_strictUnknownKeys(t *testing.T) {
	t.Parallel()
	_, err := FromMap(map[string]any{"strict_config": true, "nope": 1}, "")
	if err == nil || !strings.Contains(err.Error(), "unknown config keys") {
		t.Fatalf("err=%v", err)
	}
}

func TestFromMap_unknownKeysWarnMode(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{"nope": 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.UnknownKeys) != 1 || cfg.UnknownKeys[0] != "nope" {
		t.Fatalf("unknown=%v", cfg.UnknownKeys)
	}
}

func TestFromMap_drvfsRejected(t *testing.T) {
	t.Parallel()
	_, err := FromMap(map[string]any{"index_dir": "/mnt/c/indexes"}, "")
	if err == nil || !strings.Contains(err.Error(), "DrvFs") {
		t.Fatalf("err=%v", err)
	}
	// Double-slash spelling must still count as DrvFs (parity with paths.IsDrvFsPath).
	_, err = FromMap(map[string]any{"index_dir": "/mnt//c/indexes"}, "")
	if err == nil || !strings.Contains(err.Error(), "DrvFs") {
		t.Fatalf("double-slash DrvFs err=%v", err)
	}
	// archives_dir and archiveconverter_output_dir too
	_, err = FromMap(map[string]any{"archives_dir": "/mnt/d/stage"}, "")
	if err == nil || !strings.Contains(err.Error(), "DrvFs") {
		t.Fatalf("archives err=%v", err)
	}
	_, err = FromMap(map[string]any{"archiveconverter_output_dir": "/mnt/e/out"}, "")
	if err == nil || !strings.Contains(err.Error(), "DrvFs") {
		t.Fatalf("ac out err=%v", err)
	}
}

func TestFromMap_drvfsAllowed(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{
		"index_dir":              "/mnt/c/indexes",
		"allow_indexes_on_drvfs": true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IndexDir != "/mnt/c/indexes" {
		t.Fatalf("index_dir=%s", cfg.IndexDir)
	}
}

func TestFromMap_boolTypeEnforced(t *testing.T) {
	t.Parallel()
	_, err := FromMap(map[string]any{"write_overlay": "yes"}, "")
	if err == nil || !strings.Contains(err.Error(), "write_overlay") {
		t.Fatalf("err=%v", err)
	}
}

func TestFromMap_concurrencyMins(t *testing.T) {
	t.Parallel()
	if _, err := FromMap(map[string]any{"max_concurrent_index": 0}, ""); err == nil {
		t.Fatal("expected max_concurrent_index error")
	}
	if _, err := FromMap(map[string]any{"max_concurrent_convert": 0}, ""); err == nil {
		t.Fatal("expected max_concurrent_convert error")
	}
	cfg, err := FromMap(map[string]any{"max_concurrent_mount": 0}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrentMount != 0 {
		t.Fatalf("mount=%d", cfg.MaxConcurrentMount)
	}
}

func TestFromMap_pollIntervalMin(t *testing.T) {
	t.Parallel()
	if _, err := FromMap(map[string]any{"poll_interval_seconds": 0}, ""); err == nil {
		t.Fatal("expected poll min error")
	}
}

func TestFromMap_durationKeys(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{
		"cleanup_after":         "48h",
		"quarantine_retain_for": "1d",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CleanupAfterSeconds != 48*3600 {
		t.Fatalf("cleanup=%v", cfg.CleanupAfterSeconds)
	}
	if cfg.QuarantineRetainForSeconds != 86400 {
		t.Fatalf("quarantine=%v", cfg.QuarantineRetainForSeconds)
	}
}

func TestFromMap_dualDurationKeys(t *testing.T) {
	t.Parallel()
	// human key preferred when both present
	cfg, err := FromMap(map[string]any{
		"cleanup_after":         "2h",
		"cleanup_after_seconds": 999,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CleanupAfterSeconds != 2*3600 {
		t.Fatalf("cleanup=%v (human key should win)", cfg.CleanupAfterSeconds)
	}
	// seconds-only
	cfg, err = FromMap(map[string]any{
		"quarantine_retain_for_seconds": 3600,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QuarantineRetainForSeconds != 3600 {
		t.Fatalf("quarantine=%v", cfg.QuarantineRetainForSeconds)
	}
}

func TestFromMap_legacyAliases(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{
		"stage_archive_to":     "/var/lib/mount-wrapper/archives",
		"stage_always":         true,
		"stage_overhead_bytes": 128 * 1024 * 1024,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ArchivesDir != "/var/lib/mount-wrapper/archives" {
		t.Fatalf("archives_dir=%s", cfg.ArchivesDir)
	}
	if !cfg.MoveArchivesToLinux {
		t.Fatal("move_archives_to_linux")
	}
	if cfg.ArchiveRelocateOverheadBytes != 128*1024*1024 {
		t.Fatalf("overhead=%d", cfg.ArchiveRelocateOverheadBytes)
	}
	// strict_config should accept legacy aliases (they are known keys)
	cfg, err = FromMap(map[string]any{
		"strict_config":    true,
		"stage_archive_to": "/var/lib/mount-wrapper/archives",
	}, "")
	if err != nil {
		t.Fatalf("strict with alias: %v", err)
	}
	if cfg.ArchivesDir != "/var/lib/mount-wrapper/archives" {
		t.Fatalf("archives=%s", cfg.ArchivesDir)
	}
	// modern keys win over stage_* when both set for move flag
	cfg, err = FromMap(map[string]any{
		"move_archives_to_linux": false,
		"stage_always":           true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MoveArchivesToLinux {
		t.Fatal("move_archives_to_linux should win over stage_always")
	}
}

func TestFromMap_mountBackend(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MountBackend != BackendRust {
		t.Fatalf("default backend=%s", cfg.MountBackend)
	}
	cfg, err = FromMap(map[string]any{"mount_backend": "ratarmount"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MountBackend != BackendPython || cfg.RatarmountBin != DefaultPythonRatarmountBin {
		t.Fatalf("python alias backend=%s bin=%s", cfg.MountBackend, cfg.RatarmountBin)
	}
	cfg, err = FromMap(map[string]any{"mount_backend": "ratarmount-rs"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MountBackend != BackendRust {
		t.Fatalf("rust alias=%s", cfg.MountBackend)
	}
	if _, err := FromMap(map[string]any{"mount_backend": "go"}, ""); err == nil {
		t.Fatal("expected invalid backend error")
	}
	cfg, err = FromMap(map[string]any{
		"mount_backend":  "rust",
		"ratarmount_bin": "/opt/bin/ratarmount-rs",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveRatarmountBin() != "/opt/bin/ratarmount-rs" {
		t.Fatalf("effective=%s", cfg.EffectiveRatarmountBin())
	}
}

func TestFromMap_extraRatarmountArgs(t *testing.T) {
	t.Parallel()
	cfg, err := FromMap(map[string]any{
		"extra_ratarmount_args": []any{"--recursive"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExtraRatarmountArgs) != 1 || cfg.ExtraRatarmountArgs[0] != "--recursive" {
		t.Fatalf("args=%v", cfg.ExtraRatarmountArgs)
	}
}

func TestFromMap_sourceDirsValidation(t *testing.T) {
	t.Parallel()
	if _, err := FromMap(map[string]any{"source_dirs": []any{1}}, ""); err == nil {
		t.Fatal("expected type error")
	}
	if _, err := FromMap(map[string]any{"source_dirs": "not-a-list"}, ""); err == nil {
		t.Fatal("expected list error")
	}
}

func TestLoad_fileRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "version: 1\nsource_dirs:\n  - /tmp/inbox\nlog_level: WARNING\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "WARNING" || cfg.ConfigPath != path {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestNormalizeMountBackend(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"python":        BackendPython,
		"ratarmount":    BackendPython,
		"py":            BackendPython,
		"rust":          BackendRust,
		"ratarmount-rs": BackendRust,
		"rs":            BackendRust,
		"native":        BackendRust,
	}
	for in, want := range cases {
		got, err := NormalizeMountBackend(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q (%v) want %q", in, got, err, want)
		}
	}
}
