//go:build !unix

package cleaner

import "os"

// tryRemoveLockFile best-effort removes path (no flock on non-Unix).
func tryRemoveLockFile(path string) bool {
	return os.Remove(path) == nil
}
