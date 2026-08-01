package config

import (
	"fmt"
	"strings"
)

// Mount backend identifiers (YAML: mount_backend).
const (
	BackendPython = "python"
	BackendRust   = "rust"
)

// NormalizeMountBackend maps aliases to "python" or "rust".
//
// Accepted aliases:
//   - python, ratarmount, py, cpython
//   - rust, ratarmount-rs, ratarmount_rs, rs, native
func NormalizeMountBackend(value string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(key, "_", "-")
	switch key {
	case "python", "ratarmount", "py", "cpython":
		return BackendPython, nil
	case "rust", "ratarmount-rs", "rs", "native":
		return BackendRust, nil
	default:
		return "", &ConfigError{Message: fmt.Sprintf(
			"mount_backend: must be one of [python rust] (aliases: ratarmount, ratarmount-rs); got %q",
			value,
		)}
	}
}
