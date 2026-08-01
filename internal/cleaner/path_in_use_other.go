//go:build !linux

package cleaner

import (
	"context"
	"os/exec"
	"time"
)

// DefaultPathInUse is a best-effort check on non-Linux platforms.
// Tries `fuser -s path` when available; on any error, timeout, or missing
// binary, returns true (treat as in use) so prune never deletes a live
// materialization. Parity with tarmount-wsl cleaner.path_in_use.
func DefaultPathInUse(path string) bool {
	if path == "" {
		return true
	}
	return fuserPathInUse(path)
}

func fuserPathInUse(path string) bool {
	// fuser -s: silent; exit 0 if any process uses the path.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "fuser", "-s", path)
	err := cmd.Run()
	if ctx.Err() != nil {
		// Timeout or cancel — keep the file.
		return true
	}
	if err != nil {
		// Missing binary, non-zero exit (no users), or other failure.
		// Only exit status 0 means in-use; anything else → not in use when
		// fuser ran, or keep when fuser is unavailable.
		if _, ok := err.(*exec.Error); ok {
			// fuser not found / not executable
			return true
		}
		// ExitError: path not in use (fuser found no PIDs).
		return false
	}
	return true
}
