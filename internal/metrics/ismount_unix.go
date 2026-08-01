//go:build unix

package metrics

import (
	"os"
	"syscall"
)

func differentDevice(st, parent os.FileInfo) bool {
	s1, ok1 := st.Sys().(*syscall.Stat_t)
	s2, ok2 := parent.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return s1.Dev != s2.Dev
}
