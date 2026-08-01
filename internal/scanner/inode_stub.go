//go:build !unix

package scanner

import "os"

func inodeFromFileInfo(st os.FileInfo) (uint64, bool) {
	return 0, false
}
