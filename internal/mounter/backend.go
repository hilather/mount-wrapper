package mounter

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// Backend identifiers (YAML: mount_backend). Only rust / ratarmount-rs is supported.
const (
	BackendRust = config.BackendRust
)

// NormalizeMountBackend maps aliases to "rust".
//
// Accepted: rust, ratarmount-rs, rs, native.
// Python/ratarmount aliases are rejected.
func NormalizeMountBackend(value string) (string, error) {
	return config.NormalizeMountBackend(value)
}

// IsRustBackend reports whether backend normalizes to rust.
func IsRustBackend(backend string) bool {
	b, err := NormalizeMountBackend(backend)
	return err == nil && b == BackendRust
}

// BackendLabel is a human-readable label for logs / doctor.
func BackendLabel(backend string) string {
	b, err := NormalizeMountBackend(backend)
	if err != nil {
		return backend
	}
	if b == BackendRust {
		return "ratarmount-rs (Rust)"
	}
	return backend
}

// DefaultRatarmountBin returns the packaged/default binary name (ratarmount-rs).
func DefaultRatarmountBin(backend string) string {
	return config.DefaultRatarmountBin(backend)
}

// WhichFunc locates an executable by name (like exec.LookPath / shutil.which).
// Returns the resolved path, or empty string if not found.
type WhichFunc func(name string) string

// ExecutableFunc reports whether path is an existing executable file.
type ExecutableFunc func(path string) bool

// ResolveOptions customizes ResolveRatarmountBin (injectable for tests).
type ResolveOptions struct {
	// Which locates a binary on PATH. Nil uses a default LookPath wrapper.
	Which WhichFunc
	// IsExecutable checks a candidate path. Nil uses a default os.Stat/X_OK check.
	IsExecutable ExecutableFunc
	// SearchPath enables PATH lookup. Use SearchPathDisabled to turn off.
	SearchPathDisabled bool
	// SiblingRustRelease is an optional absolute path to a cargo release binary
	// (e.g. .../ratarmount-rs/target/release/ratarmount-rs).
	SiblingRustRelease string
	// ExtraCandidates are additional paths checked after the default and before PATH.
	ExtraCandidates []string
}

// ResolveRatarmountBin chooses the ratarmount-rs executable.
//
// Priority:
//  1. Explicit non-empty configured path (always wins, not existence-checked)
//  2. Backend default name if executable (PATH or absolute)
//  3. ExtraCandidates that are executable
//  4. SiblingRustRelease if executable
//  5. PATH search for ratarmount-rs (when search enabled)
//  6. Default name even if missing (caller/doctor report the miss)
func ResolveRatarmountBin(backend, configured string, opts ResolveOptions) (string, error) {
	b, err := NormalizeMountBackend(backend)
	if err != nil {
		return "", err
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, nil
	}

	which := opts.Which
	if which == nil {
		which = defaultWhich
	}
	isExec := opts.IsExecutable
	if isExec == nil {
		isExec = defaultIsExecutable
	}

	defaultBin := DefaultRatarmountBin(b)
	if candidate := resolveIfExecutable(defaultBin, which, isExec); candidate != "" {
		return candidate, nil
	}

	for _, c := range opts.ExtraCandidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if candidate := resolveIfExecutable(c, which, isExec); candidate != "" {
			return candidate, nil
		}
	}

	sib := strings.TrimSpace(opts.SiblingRustRelease)
	if sib != "" && isExec(sib) {
		return sib, nil
	}

	if !opts.SearchPathDisabled {
		for _, name := range []string{config.DefaultRustRatarmountBin} {
			if p := which(name); p != "" {
				return p, nil
			}
		}
	}

	return defaultBin, nil
}

func resolveIfExecutable(bin string, which WhichFunc, isExec ExecutableFunc) string {
	if bin == "" {
		return ""
	}
	if strings.Contains(bin, string(filepath.Separator)) || strings.HasPrefix(bin, ".") {
		if isExec(bin) {
			return bin
		}
		return ""
	}
	if p := which(bin); p != "" && isExec(p) {
		return p
	}
	if isExec(bin) {
		return bin
	}
	return ""
}

func defaultWhich(name string) string {
	p, err := lookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func defaultIsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
