//go:build !linux

package scanner

import (
	"log"
	"os"
	"path/filepath"

	"github.com/hilather/mount-wrapper/internal/paths"
)

// InotifyWatcher is a no-op on non-Linux platforms.
type InotifyWatcher struct{}

// NewInotifyWatcher creates an inactive watcher.
func NewInotifyWatcher() *InotifyWatcher {
	return &InotifyWatcher{}
}

// Active always returns false on non-Linux.
func (w *InotifyWatcher) Active() bool { return false }

// Start skips inotify on non-Linux.
func (w *InotifyWatcher) Start(dirs []string) []string {
	log.Printf("inotify skipped (not Linux; poll remains primary discovery)")
	return nil
}

// Poll always returns false on non-Linux.
func (w *InotifyWatcher) Poll(timeoutMs int) bool { return false }

// Close is a no-op on non-Linux.
func (w *InotifyWatcher) Close() {}

// WatchableSources returns source paths suitable for inotify (exist, not DrvFs).
func WatchableSources(mappedSources [][2]string) []string {
	var out []string
	for _, pair := range mappedSources {
		path := pair[1]
		if paths.IsDrvFsPath(path) {
			continue
		}
		st, err := os.Stat(path)
		if err != nil || !st.IsDir() {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		out = append(out, path)
	}
	return out
}
