package paths

import (
	"os"
	"os/user"
	"strconv"
)

// EnsureServiceDirectory creates path (and parents) with traversable permissions
// for FUSE mount points. Default mode is 0o755. chmod failures are ignored
// (best-effort, e.g. on some network filesystems).
func EnsureServiceDirectory(path string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o755
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	// Best-effort chmod: MkdirAll may leave existing dirs with different mode.
	_ = os.Chmod(path, mode)
	return nil
}

// EnsureServiceDirsOpts configures optional ownership for EnsureServiceDirectories.
type EnsureServiceDirsOpts struct {
	// Mode defaults to 0o755 when zero.
	Mode os.FileMode
	// Owner is a user name for best-effort chown (empty = leave uid).
	Owner string
	// Group is a group name for best-effort chown (empty = leave gid).
	Group string
}

// EnsureServiceDirectories ensures service data directories exist with consistent
// permissions. Optional owner/group chown is best-effort and no-ops when the
// name is unknown or chown is not permitted.
func EnsureServiceDirectories(paths []string, opts *EnsureServiceDirsOpts) error {
	mode := os.FileMode(0o755)
	var owner, group string
	if opts != nil {
		if opts.Mode != 0 {
			mode = opts.Mode
		}
		owner = opts.Owner
		group = opts.Group
	}

	for _, p := range paths {
		if err := EnsureServiceDirectory(p, mode); err != nil {
			return err
		}
	}

	if owner == "" && group == "" {
		return nil
	}

	uid, uidOK := nameToUID(owner)
	gid, gidOK := nameToGID(group)
	if !uidOK && !gidOK {
		return nil
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		// Go's os.Chown uses -1 to leave an id unchanged on Unix.
		useUID, useGID := -1, -1
		if uidOK {
			useUID = uid
		}
		if gidOK {
			useGID = gid
		}
		_ = os.Chown(p, useUID, useGID)
	}
	return nil
}

func nameToUID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, false
	}
	return id, true
}

func nameToGID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return id, true
}
