package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testCfg(t *testing.T, overrides map[string]any) *Config {
	t.Helper()
	raw := map[string]any{
		"source_dirs":           []any{"/var/lib/mount-wrapper/inbox"},
		"poll_interval_seconds": 60,
		"log_level":             "INFO",
		"mount_root":            "/var/lib/mount-wrapper/mounts",
		"index_dir":             "/var/lib/mount-wrapper/indexes",
		"overlay_dir":           "/var/lib/mount-wrapper/overlays",
	}
	for k, v := range overrides {
		raw[k] = v
	}
	cfg, err := FromMap(raw, "/etc/mount-wrapper/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPublicKeys_matchesToPublicMap(t *testing.T) {
	t.Parallel()
	keys := PublicKeys()
	if len(keys) < 50 {
		t.Fatalf("PublicKeys too short: %d", len(keys))
	}
	// Must be sorted and unique.
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Fatalf("PublicKeys not sorted unique at %d: %q <= %q", i, keys[i], keys[i-1])
		}
	}
	pub := ToPublicMap(&Config{})
	if len(keys) != len(pub) {
		t.Fatalf("PublicKeys len=%d ToPublicMap len=%d", len(keys), len(pub))
	}
	for _, k := range keys {
		if _, ok := pub[k]; !ok {
			t.Fatalf("PublicKeys has %q missing from ToPublicMap", k)
		}
	}
	// Core inventory keys used by parity tooling / Appendix D.
	for _, want := range []string{
		"source_dirs", "mount_root", "state_db", "mount_backend",
		"archiveconverter_enabled", "hooks_dir", "web_enabled", "log_level",
	} {
		found := false
		for _, k := range keys {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("PublicKeys missing %q", want)
		}
	}
}

func TestToPublicMap_roundtrip(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t, map[string]any{
		"cleanup_after":   "24h",
		"recursive_mount": true,
	})
	pub := ToPublicMap(cfg)
	ca, _ := pub["cleanup_after"].(string)
	if ca != "1d" && ca != "24h" {
		t.Fatalf("cleanup_after=%v", pub["cleanup_after"])
	}
	if pub["recursive_mount"] != true {
		t.Fatal("recursive_mount")
	}
	dirs, _ := pub["source_dirs"].([]string)
	if len(dirs) != 1 || dirs[0] != "/var/lib/mount-wrapper/inbox" {
		t.Fatalf("source_dirs=%v", pub["source_dirs"])
	}
	// Re-validate: convert []string back to []any for FromMap via yaml roundtrip
	text, err := ToYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatal(err)
	}
	again, err := FromMap(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.PollIntervalSeconds != 60 || !again.RecursiveMount {
		t.Fatalf("again poll=%v recursive=%v", again.PollIntervalSeconds, again.RecursiveMount)
	}
}

func TestSnapshot_shape(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t, map[string]any{"nope_unknown": 1})
	// unknown only tracked when key is unknown — testCfg overrides don't add unknown
	cfg2, err := FromMap(map[string]any{"nope": 1, "log_level": "INFO"}, "/etc/mount-wrapper/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot(cfg2)
	if _, ok := snap["config"].(map[string]any); !ok {
		t.Fatalf("config missing: %T", snap["config"])
	}
	if snap["config_path"] != "/etc/mount-wrapper/config.yaml" {
		t.Fatalf("path=%v", snap["config_path"])
	}
	hot, ok := snap["hot_reload_keys"].([]string)
	if !ok || len(hot) == 0 {
		t.Fatalf("hot_reload_keys=%v", snap["hot_reload_keys"])
	}
	// ensure expected keys present
	found := false
	for _, k := range hot {
		if k == "log_level" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("log_level not in hot_reload_keys")
	}
	restart, ok := snap["restart_required_keys"].([]string)
	if !ok || len(restart) == 0 {
		t.Fatalf("restart_required_keys=%v", snap["restart_required_keys"])
	}
	found = false
	for _, k := range restart {
		if k == "mount_root" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("mount_root not in restart_required_keys")
	}
	unk, _ := snap["unknown_keys"].([]string)
	if len(unk) != 1 || unk[0] != "nope" {
		t.Fatalf("unknown_keys=%v", snap["unknown_keys"])
	}
	_ = cfg
}

func TestMergePatch(t *testing.T) {
	t.Parallel()
	base := ToPublicMap(testCfg(t, nil))
	merged, err := MergePatch(base, map[string]any{
		"poll_interval_seconds": 30,
		"log_level":             "WARNING",
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged["poll_interval_seconds"] != 30 {
		t.Fatalf("poll=%v", merged["poll_interval_seconds"])
	}
	if merged["log_level"] != "WARNING" {
		t.Fatalf("log=%v", merged["log_level"])
	}
	// source_dirs preserved
	if !publicEqual(merged["source_dirs"], base["source_dirs"]) {
		t.Fatalf("source_dirs changed: %v", merged["source_dirs"])
	}
}

func TestClassifyChanges_hotVsRestart(t *testing.T) {
	t.Parallel()
	old := testCfg(t, nil)
	new := testCfg(t, map[string]any{
		"poll_interval_seconds": 10,
		"mount_root":            "/var/lib/mount-wrapper/mounts2",
	})
	changed, hot, restart := ClassifyChanges(old, new)
	has := func(ss []string, k string) bool {
		for _, s := range ss {
			if s == k {
				return true
			}
		}
		return false
	}
	if !has(changed, "poll_interval_seconds") || !has(hot, "poll_interval_seconds") {
		t.Fatalf("changed=%v hot=%v", changed, hot)
	}
	if !has(restart, "mount_root") {
		t.Fatalf("restart=%v", restart)
	}
	if _, ok := RestartRequiredKeys["mount_root"]; !ok {
		t.Fatal("mount_root should be restart required")
	}
}

func TestWriteFile_atomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := testCfg(t, map[string]any{"log_level": "DEBUG"})
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "log_level") {
		t.Fatalf("content=%s", data)
	}
	// no leftover temp files
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp: %s", e.Name())
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != "DEBUG" {
		t.Fatalf("log=%s", loaded.LogLevel)
	}
}

func TestApplyUpdate_dryRun(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyUpdate(cfg, map[string]any{"poll_interval_seconds": 15}, nil, false, path)
	if err != nil {
		t.Fatal(err)
	}
	if result["valid"] != true || result["written"] != false {
		t.Fatalf("result=%v", result)
	}
	changed, _ := result["changed_keys"].([]string)
	if !contains(changed, "poll_interval_seconds") {
		t.Fatalf("changed=%v", changed)
	}
	hot, _ := result["hot_reloadable"].([]string)
	if !contains(hot, "poll_interval_seconds") {
		t.Fatalf("hot=%v", hot)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.PollIntervalSeconds != 60 {
		t.Fatalf("file changed on dry-run: poll=%v", again.PollIntervalSeconds)
	}
}

func TestApplyUpdate_writesHot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyUpdate(cfg, map[string]any{
		"log_level":             "DEBUG",
		"poll_interval_seconds": 5,
	}, nil, true, path)
	if err != nil {
		t.Fatal(err)
	}
	if result["written"] != true {
		t.Fatalf("written=%v", result["written"])
	}
	restart, _ := result["restart_required"].([]string)
	if len(restart) != 0 {
		t.Fatalf("restart=%v", restart)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != "DEBUG" || loaded.PollIntervalSeconds != 5 {
		t.Fatalf("loaded log=%s poll=%v", loaded.LogLevel, loaded.PollIntervalSeconds)
	}
}

func TestApplyUpdate_fullConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	full := ToPublicMap(cfg)
	// FromMap after YAML expects []any for lists; convert via yaml
	text, _ := yaml.Marshal(full)
	var fullRaw map[string]any
	_ = yaml.Unmarshal(text, &fullRaw)
	fullRaw["source_dirs"] = []any{"/tmp/inbox"}
	fullRaw["name_regex"] = `.*\.zip$`
	result, err := ApplyUpdate(cfg, nil, fullRaw, true, path)
	if err != nil {
		t.Fatal(err)
	}
	if result["written"] != true {
		t.Fatal("not written")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SourceDirs) != 1 || loaded.SourceDirs[0] != "/tmp/inbox" {
		t.Fatalf("dirs=%v", loaded.SourceDirs)
	}
	if loaded.NameRegex != `.*\.zip$` {
		t.Fatalf("regex=%s", loaded.NameRegex)
	}
}

func TestApplyUpdate_restartRequired(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyUpdate(cfg, map[string]any{
		"state_db":        "/var/lib/mount-wrapper/other.db",
		"windows_visible": false,
	}, nil, true, path)
	if err != nil {
		t.Fatal(err)
	}
	restart, _ := result["restart_required"].([]string)
	if !contains(restart, "state_db") || !contains(restart, "windows_visible") {
		t.Fatalf("restart=%v", restart)
	}
}

func TestApplyUpdate_invalidPatch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyUpdate(cfg, map[string]any{"stable_file_mode": "nope"}, nil, false, path)
	if err == nil || !strings.Contains(err.Error(), "stable_file_mode") {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyUpdate_drvfsGuard(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testCfg(t, nil)
	if err := WriteFile(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyUpdate(cfg, map[string]any{"index_dir": "/mnt/c/indexes"}, nil, true, path)
	if err == nil || !strings.Contains(err.Error(), "DrvFs") {
		t.Fatalf("err=%v", err)
	}
}

func contains(ss []string, k string) bool {
	for _, s := range ss {
		if s == k {
			return true
		}
	}
	return false
}
