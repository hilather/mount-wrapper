package reconcile

import (
	"os"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
)

func (p Probes) withDefaults() Probes {
	if p.IsMount == nil {
		p.IsMount = func(string) bool { return false }
	}
	if p.PIDAlive == nil {
		p.PIDAlive = mounter.IsProcessAlive
	}
	if p.PathExists == nil {
		p.PathExists = pathIsRegularFile
	}
	if p.IndexIsFile == nil {
		p.IndexIsFile = pathIsRegularFile
	}
	if p.Clock == nil {
		p.Clock = func() float64 {
			return float64(time.Now().UnixNano()) / 1e9
		}
	}
	return p
}

func pathIsRegularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}
