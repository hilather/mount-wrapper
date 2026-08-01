package mounter

import (
	"path/filepath"
	"strconv"
	"strings"
)

// FormatRecursiveMountExtensions joins extension rules for
// ratarmount --recursive-extensions.
func FormatRecursiveMountExtensions(extensions []string) string {
	return strings.Join(extensions, ",")
}

// EffectiveRatarmountExtraArgs returns configured extras without injecting a
// default --use-backend (parity with Python effective_ratarmount_extra_args).
func EffectiveRatarmountExtraArgs(extraArgs []string) []string {
	if len(extraArgs) == 0 {
		return nil
	}
	out := make([]string, len(extraArgs))
	copy(out, extraArgs)
	return out
}

// BuildRatarmountCmd builds the ratarmount / ratarmount-rs argv for req.
// Does not start a process and does not create directories.
//
// Shared flags (both backends): -f, --index-file, -P, optional -d / --log-file,
// --recursive / --recursive-extensions, --write-overlay, -o allow_other,
// extra args (with de-dupe of recursive / no-mount), optional --no-mount,
// archive path, mount path.
func BuildRatarmountCmd(req MountRequest) []string {
	cmd := []string{
		req.RatarmountBin,
		"-f",
		"--index-file",
		req.IndexPath,
		"-P",
		strconv.Itoa(req.IndexWorkers),
	}
	if req.RatarmountDebug > 0 {
		d := req.RatarmountDebug
		if d > 3 {
			d = 3
		}
		cmd = append(cmd, "-d", strconv.Itoa(d))
	}
	if dir := strings.TrimSpace(req.RatarmountLogDir); dir != "" {
		logFile := filepath.Join(dir, req.ArchiveID+".ratarmount.log")
		cmd = append(cmd, "--log-file", logFile)
	}
	if req.RecursiveMount {
		// Nested supported archives become browsable. Only takes effect when
		// building a new index (ratarmount ignores --recursive if index exists).
		cmd = append(cmd, "--recursive")
	}
	if req.OverlayPath != "" {
		cmd = append(cmd, "--write-overlay", req.OverlayPath)
	}
	if req.AllowOther {
		cmd = append(cmd, "-o", "allow_other")
	}

	extras := EffectiveRatarmountExtraArgs(req.ExtraArgs)
	if req.RecursiveMount {
		extras = filterRecursiveFromExtras(extras)
	}
	extras = filterMountModeFromExtras(extras)
	cmd = append(cmd, extras...)

	if req.RecursiveMount && len(req.RecursiveMountExtensions) > 0 {
		cmd = append(cmd,
			"--recursive-extensions",
			FormatRecursiveMountExtensions(req.RecursiveMountExtensions),
		)
	}
	if req.IndexOnly {
		cmd = append(cmd, "--no-mount")
	}
	cmd = append(cmd, req.ArchivePath, req.MountPath)
	return cmd
}

func filterRecursiveFromExtras(extras []string) []string {
	if len(extras) == 0 {
		return nil
	}
	out := make([]string, 0, len(extras))
	skipNext := false
	for _, a := range extras {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "--recursive" || a == "-r" {
			continue
		}
		if a == "--recursive-extensions" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "--recursive-extensions=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func filterMountModeFromExtras(extras []string) []string {
	if len(extras) == 0 {
		return nil
	}
	out := make([]string, 0, len(extras))
	for _, a := range extras {
		if a == "--no-mount" || a == "--mount" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// ChildEnvOptions controls environment variables passed to ratarmount children.
type ChildEnvOptions struct {
	// Base is the starting environment (typically os.Environ()). When nil, an
	// empty env is used (callers should pass os.Environ() in production).
	Base []string
	// Ratarmount7zDebug sets RATARMOUNT_7Z_DEBUG=1 when true.
	Ratarmount7zDebug bool
	// RatarmountRustLog, when non-empty, sets RUST_LOG.
	RatarmountRustLog string
}

// BuildChildEnv returns the environment for a ratarmount child process.
// Keys already present in Base are replaced when set by this function.
func BuildChildEnv(opts ChildEnvOptions) []string {
	env := append([]string(nil), opts.Base...)
	if opts.Ratarmount7zDebug {
		env = setEnv(env, "RATARMOUNT_7Z_DEBUG", "1")
	}
	if v := strings.TrimSpace(opts.RatarmountRustLog); v != "" {
		env = setEnv(env, "RUST_LOG", v)
	}
	return env
}

// ChildEnvFromConfig is a convenience wrapper over BuildChildEnv.
func ChildEnvFromConfig(base []string, ratarmount7zDebug bool, ratarmountRustLog string) []string {
	return BuildChildEnv(ChildEnvOptions{
		Base:              base,
		Ratarmount7zDebug: ratarmount7zDebug,
		RatarmountRustLog: ratarmountRustLog,
	})
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
