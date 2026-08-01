package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// MergePatch shallow-merges patch over base public config mapping.
// Nested structures are replaced wholesale (source_dirs, extra_ratarmount_args).
func MergePatch(base, patch map[string]any) (map[string]any, error) {
	if patch == nil {
		return nil, configErrorf("config patch must be a mapping")
	}
	merged := make(map[string]any, len(base)+len(patch))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range patch {
		merged[k] = v
	}
	return merged, nil
}

// ClassifyChanges compares two configs.
// Returns (changedKeys, hotReloadable, restartRequired) using public YAML key names.
func ClassifyChanges(old, new *Config) (changed, hot, restart []string) {
	oldD := ToPublicMap(old)
	newD := ToPublicMap(new)
	keys := make(map[string]struct{}, len(oldD)+len(newD))
	for k := range oldD {
		keys[k] = struct{}{}
	}
	for k := range newD {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, key := range sorted {
		if !publicEqual(oldD[key], newD[key]) {
			changed = append(changed, key)
		}
	}
	for _, key := range changed {
		if _, ok := RestartRequiredKeys[key]; ok {
			restart = append(restart, key)
		} else {
			// Known hot keys and any other public keys default to hot-reload.
			hot = append(hot, key)
		}
	}
	return changed, hot, restart
}

func publicEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeForCompare(a), normalizeForCompare(b))
}

// normalizeForCompare reduces int/float variance so 60 and 60.0 compare equal.
func normalizeForCompare(v any) any {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case float32:
		return float64(n)
	case float64:
		return n
	case []string:
		out := make([]any, len(n))
		for i, s := range n {
			out[i] = s
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, item := range n {
			out[i] = normalizeForCompare(item)
		}
		return out
	default:
		return v
	}
}

// ToYAML dumps the public config dict as YAML text.
func ToYAML(cfg *Config) (string, error) {
	pub := ToPublicMap(cfg)
	data, err := yaml.Marshal(pub)
	if err != nil {
		return "", configErrorf("cannot marshal config YAML: %v", err)
	}
	return string(data), nil
}

// WriteFile atomically writes cfg as YAML to path (temp file + rename).
func WriteFile(path string, cfg *Config, createParents bool) error {
	if path == "" {
		return configErrorf("cannot write config: empty path")
	}
	if createParents {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return configErrorf("cannot create config parent dir: %v", err)
		}
	}
	text, err := ToYAML(cfg)
	if err != nil {
		return err
	}
	if len(text) == 0 || text[len(text)-1] != '\n' {
		text += "\n"
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return configErrorf("cannot create temp config file: %v", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return configErrorf("cannot write temp config file: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return configErrorf("cannot sync temp config file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return configErrorf("cannot close temp config file: %v", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return configErrorf("cannot replace config file: %v", err)
	}
	success = true
	return nil
}

// ApplyUpdate validates and optionally writes a config update.
// Provide either full (complete mapping) or patch (shallow merge onto current public dict).
// Returns a result map suitable for control-plane data.
func ApplyUpdate(current *Config, patch, full map[string]any, apply bool, configPath string) (map[string]any, error) {
	if current == nil {
		return nil, configErrorf("current config is required")
	}
	if full != nil && patch != nil {
		return nil, configErrorf("provide either full config or patch, not both")
	}
	if full == nil && patch == nil {
		return nil, configErrorf("missing config or patch")
	}

	path := configPath
	if path == "" {
		path = current.ConfigPath
	}
	if apply && path == "" {
		return nil, configErrorf("no config_path available to write")
	}

	base := ToPublicMap(current)
	var raw map[string]any
	var err error
	if full != nil {
		raw = make(map[string]any, len(full))
		for k, v := range full {
			raw[k] = v
		}
	} else {
		raw, err = MergePatch(base, patch)
		if err != nil {
			return nil, err
		}
	}
	if _, ok := raw["version"]; !ok {
		raw["version"] = current.Version
	}

	pathForCfg := path
	newCfg, err := FromMap(raw, pathForCfg)
	if err != nil {
		return nil, err
	}

	changed, hot, restart := ClassifyChanges(current, newCfg)

	var pathVal any
	if path != "" {
		pathVal = path
	}

	result := map[string]any{
		"valid":            true,
		"apply":            apply,
		"changed_keys":     changed,
		"hot_reloadable":   hot,
		"restart_required": restart,
		"config":           ToPublicMap(newCfg),
		"config_path":      pathVal,
		"written":          false,
		"reload_scheduled": false,
	}

	if !apply {
		return result, nil
	}

	if path == "" {
		return nil, configErrorf("cannot apply: config_path is empty")
	}

	// Preserve path on written config object for subsequent reloads.
	pub := ToPublicMap(newCfg)
	written, err := FromMap(pub, path)
	if err != nil {
		return nil, err
	}
	if err := WriteFile(path, written, true); err != nil {
		return nil, err
	}
	result["written"] = true
	result["config"] = ToPublicMap(written)
	result["reload_recommended"] = len(hot) > 0 || len(changed) > 0
	return result, nil
}

// ValidateMapping validates a raw mapping into Config.
func ValidateMapping(raw map[string]any, configPath string) (*Config, error) {
	return FromMap(raw, configPath)
}
