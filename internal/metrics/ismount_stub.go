//go:build !unix

package metrics

import "os"

func differentDevice(st, parent os.FileInfo) bool {
	// No device comparison on non-unix; never treat as mount via this heuristic.
	_, _ = st, parent
	return false
}
