//go:build !unix

package cleaner

// diskFreeBytes is unavailable on non-unix platforms.
func diskFreeBytes(path string) (free int64, ok bool) {
	return 0, false
}
