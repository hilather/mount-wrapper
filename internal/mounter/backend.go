package mounter

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// Backend identifiers (YAML: mount_backend). Re-exported for callers that
// import mounter without pulling config for constants only.
const (
	BackendPython = config.BackendPython
	BackendRust   = config.BackendRust
)

// NormalizeMountBackend maps aliases to "python" or "rust".
//
// Accepted aliases (parity with config.NormalizeMountBackend / backends.py):
//   - python, ratarmount, py, cpython
//   - rust, ratarmount-rs, ratarmount_rs, rs, native
func NormalizeMountBackend(value string) (string, error) {
	return config.NormalizeMountBackend(value)
}

// IsPythonBackend reports whether backend normalizes to python.
func IsPythonBackend(backend string) bool {
	b, err := NormalizeMountBackend(backend)
	return err == nil && b == BackendPython
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
	return "ratarmount (Python)"
}

// DefaultRatarmountBin returns the packaged/default binary name for backend.
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
	// SearchPath enables PATH lookup (default true when zero-value false is
	// awkward — use SearchPathDisabled to turn off).
	SearchPathDisabled bool
	// SiblingRustRelease is an optional absolute path to a cargo release binary
	// (e.g. .../ratarmount-rs/target/release/ratarmount). Checked for rust only.
	SiblingRustRelease string
	// ExtraCandidates are additional paths checked (in order) after the default
	// path and before PATH search. Useful for ~/.local/bin without hardcoding
	// machine-private paths in production callers.
	ExtraCandidates []string
}

// ResolveRatarmountBin chooses the ratarmount executable for backend.
//
// Priority (parity with backends.resolve_ratarmount_bin):
//  1. Explicit non-empty configured path (always wins, not existence-checked)
//  2. Backend default path/name if executable (absolute path or on PATH)
//  3. ExtraCandidates that are executable
//  4. For rust: SiblingRustRelease if executable
//  5. PATH search for backend binary names (when search enabled)
//  6. Backend default name/path even if missing (caller/doctor report the miss)
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

	if b == BackendRust {
		sib := strings.TrimSpace(opts.SiblingRustRelease)
		if sib != "" && isExec(sib) {
			return sib, nil
		}
	}

	if !opts.SearchPathDisabled {
		// Prefer backend-specific PATH names, then the common "ratarmount".
		names := backendPATHNames(b)
		for _, name := range names {
			if p := which(name); p != "" {
				return p, nil
			}
		}
	}

	return defaultBin, nil
}

func backendPATHNames(backend string) []string {
	if backend == BackendPython {
		return []string{config.DefaultPythonRatarmountBin, "ratarmount"}
	}
	// rust: try ratarmount-rs then plain ratarmount (cargo install name often plain)
	return []string{config.DefaultRustRatarmountBin, "ratarmount"}
}

func resolveIfExecutable(bin string, which WhichFunc, isExec ExecutableFunc) string {
	if bin == "" {
		return ""
	}
	// Absolute or relative path with a separator: check as path.
	if strings.Contains(bin, string(filepath.Separator)) || strings.HasPrefix(bin, ".") {
		if isExec(bin) {
			return bin
		}
		return ""
	}
	// Bare name: if Which finds it and it is executable, use resolved path.
	if p := which(bin); p != "" && isExec(p) {
		return p
	}
	// Bare name that is itself a path that happens to exist (rare).
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
	// Best-effort execute bit check (Unix).
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
