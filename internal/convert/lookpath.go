package convert

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WhichFunc locates an executable by name (like exec.LookPath / shutil.which).
// Returns the resolved path, or empty string if not found.
type WhichFunc func(name string) string

// ExecutableFunc reports whether path is an existing executable file.
type ExecutableFunc func(path string) bool

// ResolveOptions customizes binary resolution (injectable for tests).
type ResolveOptions struct {
	// Which locates a binary on PATH. Nil uses exec.LookPath.
	Which WhichFunc
	// IsExecutable checks a candidate path. Nil uses os.Stat + X_OK.
	IsExecutable ExecutableFunc
	// SearchPathDisabled skips PATH lookup.
	SearchPathDisabled bool
	// SiblingRelease is an optional absolute path to a cargo release binary
	// (e.g. .../archiveconverter/target/release/archiveconverter).
	SiblingRelease string
	// ExtraCandidates are checked after the default home path and before PATH.
	ExtraCandidates []string
}

func whichOf(opts ResolveOptions) WhichFunc {
	if opts.Which != nil {
		return opts.Which
	}
	return defaultWhich
}

func execOf(opts ResolveOptions) ExecutableFunc {
	if opts.IsExecutable != nil {
		return opts.IsExecutable
	}
	return defaultIsExecutable
}

func defaultWhich(name string) string {
	p, err := exec.LookPath(name)
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

// resolveIfExecutable returns path when it is executable (absolute path or bare name on PATH).
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
