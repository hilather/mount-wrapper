//go:build !unix

package archives

// diskFreeBytes is unavailable on non-unix platforms; CheckRelocateSpace
// treats unknown free space as permissive (parity with Python None).
func diskFreeBytes(path string) (free int64, ok bool) {
	_ = path
	return 0, false
}
