//go:build !unix

package mounter

// DefaultIsMount is a no-op stub on non-Unix platforms (always false).
func DefaultIsMount(path string) bool {
	return false
}
