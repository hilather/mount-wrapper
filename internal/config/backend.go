package config

import (
	"fmt"
	"strings"
)

// Mount backend identifiers (YAML: mount_backend).
// Only the Rust engine (ratarmount-rs) is supported.
const (
	BackendRust = "rust"
)

// NormalizeMountBackend maps aliases to "rust".
//
// Accepted aliases:
//   - rust, ratarmount-rs, ratarmount_rs, rs, native
//
// Python ratarmount is not supported; those aliases return an error.
func NormalizeMountBackend(value string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(key, "_", "-")
	switch key {
	case "", "rust", "ratarmount-rs", "rs", "native":
		return BackendRust, nil
	case "python", "ratarmount", "py", "cpython":
		return "", &ConfigError{Message: fmt.Sprintf(
			"mount_backend: python/ratarmount is no longer supported; use rust (ratarmount-rs); got %q",
			value,
		)}
	default:
		return "", &ConfigError{Message: fmt.Sprintf(
			"mount_backend: must be rust (aliases: ratarmount-rs, rs, native); got %q",
			value,
		)}
	}
}
