package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SecurityPolicy controls ownership and path-escape checks for hooks.d.
//
// Production packaging expects root-owned hooks.d (RequireRootOwner=true).
// AllowedOwnerUIDs may list the service user so non-root service installs can
// own scripts (root is always accepted when any ownership check is active).
// Tests set RequireRootOwner=false with empty AllowedOwnerUIDs so non-root CI
// can still exercise group/other-writable and symlink-escape checks.
type SecurityPolicy struct {
	// RequireRootOwner requires uid 0 unless the owner is listed in
	// AllowedOwnerUIDs. Default for production packaging: true.
	RequireRootOwner bool

	// AllowedOwnerUIDs are additional UIDs permitted as owners (e.g. service user).
	// When RequireRootOwner is false and this is empty, ownership is not checked.
	AllowedOwnerUIDs []uint32

	// RequireUnderHooksDir requires the realpath of each hook to stay under
	// realpath(hooks.d). Default true (strict v1).
	RequireUnderHooksDir bool
}

// DefaultSecurityPolicy returns a production-oriented policy.
//
// When running as root, only root-owned hooks.d / scripts are accepted.
// When running as a non-root service user, root-owned (packaging default) or
// process-uid-owned scripts are accepted — not arbitrary UIDs. Path-escape and
// g+w/o+w checks are always on. Unit tests should use TestSecurityPolicy.
func DefaultSecurityPolicy() SecurityPolicy {
	uid := uint32(os.Geteuid())
	p := SecurityPolicy{
		RequireRootOwner:     uid == 0,
		RequireUnderHooksDir: true,
	}
	if uid != 0 {
		p.AllowedOwnerUIDs = []uint32{uid}
	}
	return p
}

// TestSecurityPolicy returns a policy suitable for non-root unit tests
// (no owner check; path escape and writable-bit checks remain).
func TestSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		RequireRootOwner:     false,
		RequireUnderHooksDir: true,
	}
}

func (p SecurityPolicy) ownerOK(uid uint32) bool {
	if p.RequireRootOwner {
		if uid == 0 {
			return true
		}
		for _, u := range p.AllowedOwnerUIDs {
			if uid == u {
				return true
			}
		}
		return false
	}
	// Relaxed: optional allow-list; empty means any owner.
	if len(p.AllowedOwnerUIDs) == 0 {
		return true
	}
	if uid == 0 {
		return true
	}
	for _, u := range p.AllowedOwnerUIDs {
		if uid == u {
			return true
		}
	}
	return false
}

func (p SecurityPolicy) ownerFailMessage(uid uint32, path string) string {
	if p.RequireRootOwner && len(p.AllowedOwnerUIDs) == 0 {
		return fmt.Sprintf("must be owned by root (uid 0), got uid=%d: %s", uid, path)
	}
	return fmt.Sprintf("owner uid=%d not allowed for %s", uid, path)
}

// ValidateHooksDir validates hooks.d itself. Returns SecurityError on failure.
func ValidateHooksDir(hooksDir string, policy SecurityPolicy) error {
	fi, err := os.Lstat(hooksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return securityErrorf("hooks_dir does not exist: %s", hooksDir)
		}
		return securityErrorf("cannot stat hooks_dir %s: %v", hooksDir, err)
	}
	if !fi.IsDir() {
		return securityErrorf("hooks_dir is not a directory: %s", hooksDir)
	}
	// Follow one level for real dir (hooks.d should not be a symlink escape).
	st, err := os.Stat(hooksDir)
	if err != nil {
		return securityErrorf("cannot stat hooks_dir %s: %v", hooksDir, err)
	}
	if !st.IsDir() {
		return securityErrorf("hooks_dir is not a directory: %s", hooksDir)
	}
	uid, mode, err := fileOwnerMode(st)
	if err != nil {
		return securityErrorf("cannot read hooks_dir ownership %s: %v", hooksDir, err)
	}
	if !policy.ownerOK(uid) {
		return securityErrorf("hooks_dir %s", policy.ownerFailMessage(uid, hooksDir))
	}
	if mode&0o020 != 0 {
		return securityErrorf("hooks_dir must not be group-writable: %s", hooksDir)
	}
	if mode&0o002 != 0 {
		return securityErrorf("hooks_dir must not be other-writable: %s", hooksDir)
	}
	return nil
}

// ValidateHookSecurity validates one hook path and returns the resolved real
// path to execute.
//
// Checks: regular file after resolve; realpath under hooks.d when required;
// owner policy; no group/other write bits; executable.
func ValidateHookSecurity(hookPath, hooksDir string, policy SecurityPolicy) (string, error) {
	hooksReal, err := filepath.EvalSymlinks(hooksDir)
	if err != nil {
		// Fall back to Abs+Clean if EvalSymlinks fails (missing intermediate).
		hooksReal, err = filepath.Abs(hooksDir)
		if err != nil {
			return "", securityErrorf("cannot resolve hooks_dir %s: %v", hooksDir, err)
		}
		hooksReal = filepath.Clean(hooksReal)
	}

	lst, err := os.Lstat(hookPath)
	if err != nil {
		return "", securityErrorf("cannot stat hook %s: %v", hookPath, err)
	}

	var real string
	if lst.Mode()&os.ModeSymlink != 0 {
		real, err = filepath.EvalSymlinks(hookPath)
		if err != nil {
			return "", securityErrorf("cannot resolve hook symlink %s: %v", hookPath, err)
		}
	} else {
		if !lst.Mode().IsRegular() {
			return "", securityErrorf("hook is not a regular file: %s", hookPath)
		}
		real, err = filepath.EvalSymlinks(hookPath)
		if err != nil {
			// Non-symlink regular file: Abs is enough if no parent symlinks.
			real, err = filepath.Abs(hookPath)
			if err != nil {
				return "", securityErrorf("cannot resolve hook %s: %v", hookPath, err)
			}
			real = filepath.Clean(real)
		}
	}

	st, err := os.Stat(real)
	if err != nil {
		return "", securityErrorf("cannot stat resolved hook %s: %v", real, err)
	}
	if !st.Mode().IsRegular() {
		return "", securityErrorf("hook target is not a regular file: %s", real)
	}

	if policy.RequireUnderHooksDir {
		// Ensure real is under hooksReal (path escape protection).
		rel, err := filepath.Rel(hooksReal, real)
		if err != nil || rel == ".." || hasParentRel(rel) {
			return "", securityErrorf("hook realpath escapes hooks_dir: %s not under %s", real, hooksReal)
		}
	}

	uid, mode, err := fileOwnerMode(st)
	if err != nil {
		return "", securityErrorf("cannot read hook ownership %s: %v", real, err)
	}
	if !policy.ownerOK(uid) {
		return "", securityErrorf("hook %s", policy.ownerFailMessage(uid, real))
	}
	if mode&0o020 != 0 {
		return "", securityErrorf("hook must not be group-writable: %s", real)
	}
	if mode&0o002 != 0 {
		return "", securityErrorf("hook must not be other-writable: %s", real)
	}
	if mode&0o111 == 0 {
		return "", securityErrorf("hook is not executable: %s", real)
	}
	return real, nil
}

func hasParentRel(rel string) bool {
	// filepath.Rel returns paths like "../x" when outside.
	return rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) ||
		// also catch embedded /../ (should not appear from Rel, but be safe)
		containsDotDot(rel)
}

func containsDotDot(rel string) bool {
	for _, p := range splitPath(rel) {
		if p == ".." {
			return true
		}
	}
	return false
}

func splitPath(p string) []string {
	var out []string
	for p != "" {
		var seg string
		i := 0
		for i < len(p) && p[i] != '/' && p[i] != filepath.Separator {
			i++
		}
		seg = p[:i]
		if seg != "" {
			out = append(out, seg)
		}
		if i >= len(p) {
			break
		}
		p = p[i+1:]
	}
	return out
}

func fileOwnerMode(fi os.FileInfo) (uid uint32, mode os.FileMode, err error) {
	mode = fi.Mode().Perm()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, mode, fmt.Errorf("unsupported file info sys type %T", fi.Sys())
	}
	return st.Uid, mode, nil
}
