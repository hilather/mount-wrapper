//go:build unix

package scanner

import (
	"os"
	"syscall"
)

func inodeFromFileInfo(st os.FileInfo) (uint64, bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys == nil {
		return 0, false
	}
	return sys.Ino, true
}
