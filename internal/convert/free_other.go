//go:build !unix

package convert

// diskFreeBytes is unavailable on non-unix platforms; space gates allow convert.
func diskFreeBytes(path string) (free int64, ok bool) {
	return 0, false
}
