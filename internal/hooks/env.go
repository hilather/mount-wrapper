package hooks

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/state"
)

// ArchiveEnv holds the archive fields used to build the MOUNT_WRAPPER_* env map.
// Callers may pass a *state.ArchiveRecord via FromArchiveRecord.
type ArchiveEnv struct {
	ArchiveID       string
	ArchivePath     string
	MountPath       string
	IndexPath       string
	OverlayPath     string
	ArchiveBasename string
	SourceDir       string
}

// FromArchiveRecord maps a state row into ArchiveEnv.
func FromArchiveRecord(rec *state.ArchiveRecord) ArchiveEnv {
	if rec == nil {
		return ArchiveEnv{}
	}
	return ArchiveEnv{
		ArchiveID:       rec.ArchiveID,
		ArchivePath:     rec.ArchivePath,
		MountPath:       derefStr(rec.MountPath),
		IndexPath:       derefStr(rec.IndexPath),
		OverlayPath:     derefStr(rec.OverlayPath),
		ArchiveBasename: rec.ArchiveBasename,
		SourceDir:       rec.SourceDir,
	}
}

// BuildHookEnv builds the process environment for a hook (MOUNT_WRAPPER_* only
// for mount-wrapper fields). baseEnv is typically os.Environ(); when nil, the
// current process environment is used. Existing MOUNT_WRAPPER_* keys in base
// are overwritten by archive fields.
func BuildHookEnv(rec ArchiveEnv, hookName, configPath string, baseEnv []string) map[string]string {
	env := environMap(baseEnv)
	env[EnvPrefix+"ARCHIVE_ID"] = rec.ArchiveID
	env[EnvPrefix+"ARCHIVE_PATH"] = rec.ArchivePath
	env[EnvPrefix+"MOUNT_PATH"] = rec.MountPath
	env[EnvPrefix+"INDEX_PATH"] = rec.IndexPath
	env[EnvPrefix+"OVERLAY_PATH"] = rec.OverlayPath
	env[EnvPrefix+"ARCHIVE_BASENAME"] = rec.ArchiveBasename
	env[EnvPrefix+"SOURCE_DIR"] = rec.SourceDir
	env[EnvPrefix+"CONFIG"] = configPath
	env[EnvPrefix+"HOOK_NAME"] = hookName
	return env
}

// EnvSlice converts an env map to KEY=VALUE strings for exec.Cmd.Env.
func EnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// ResolveHooksCwd resolves the working directory for hooks from config hooks_cwd.
func ResolveHooksCwd(rec ArchiveEnv, hooksCwd, hooksDir string) string {
	switch hooksCwd {
	case config.HooksCwdArchiveDir:
		if rec.ArchivePath == "" {
			return "."
		}
		return filepath.Dir(rec.ArchivePath)
	case config.HooksCwdHooksDir:
		return hooksDir
	case config.HooksCwdMount, "":
		if rec.MountPath != "" {
			return rec.MountPath
		}
		return "."
	default:
		if rec.MountPath != "" {
			return rec.MountPath
		}
		return "."
	}
}

// HookArgv returns argv for a hook: [hookPath, mountPath, archivePath].
func HookArgv(hookPath string, rec ArchiveEnv) []string {
	return []string{hookPath, rec.MountPath, rec.ArchivePath}
}

func environMap(base []string) map[string]string {
	if base == nil {
		base = os.Environ()
	}
	m := make(map[string]string, len(base)+16)
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
