//go:build linux

package scanner

import (
	"log"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/hilather/mount-wrapper/internal/paths"
	"golang.org/x/sys/unix"
)

// Inotify event mask (parity with Python InotifyWatcher).
const (
	inCloseWrite = unix.IN_CLOSE_WRITE
	inMovedTo    = unix.IN_MOVED_TO
	inMovedFrom  = unix.IN_MOVED_FROM
	inDelete     = unix.IN_DELETE
	inOnlyDir    = unix.IN_ONLYDIR
	inQOverflow  = unix.IN_Q_OVERFLOW
)

var inotifyMask = uint32(inCloseWrite | inMovedTo | inMovedFrom | inDelete | inOnlyDir)

// InotifyWatcher is a lightweight directory watcher using Linux inotify.
// Poll remains authoritative; this only signals that a rescan may be useful.
// DrvFs paths (/mnt/<letter>) are never watched.
type InotifyWatcher struct {
	fd       int
	wdToPath map[int]string
}

// NewInotifyWatcher creates an inactive watcher.
func NewInotifyWatcher() *InotifyWatcher {
	return &InotifyWatcher{fd: -1, wdToPath: make(map[int]string)}
}

// Active reports whether the watcher has an open inotify fd.
func (w *InotifyWatcher) Active() bool {
	return w != nil && w.fd >= 0
}

// Start watches non-DrvFs directories that exist. Returns paths actually watched.
func (w *InotifyWatcher) Start(dirs []string) []string {
	w.Close()
	var watched []string

	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		log.Printf("inotify unavailable: %v", err)
		return watched
	}
	w.fd = fd
	w.wdToPath = make(map[int]string)

	for _, d := range dirs {
		path := d
		if paths.IsDrvFsPath(path) {
			log.Printf("inotify skip DrvFs path %s", path)
			continue
		}
		st, err := os.Stat(path)
		if err != nil || !st.IsDir() {
			continue
		}
		wd, err := unix.InotifyAddWatch(w.fd, path, inotifyMask)
		if err != nil {
			log.Printf("inotify_add_watch failed for %s: %v", path, err)
			continue
		}
		w.wdToPath[wd] = path
		watched = append(watched, path)
		log.Printf("inotify watching %s", path)
	}
	if len(w.wdToPath) == 0 {
		w.Close()
	}
	return watched
}

// Poll returns true if any relevant event was seen (or queue overflow).
func (w *InotifyWatcher) Poll(timeoutMs int) bool {
	if w == nil || w.fd < 0 {
		return false
	}
	pfd := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	n, err := unix.Poll(pfd, timeoutMs)
	if err != nil || n == 0 {
		return false
	}
	buf := make([]byte, 65536)
	nr, err := unix.Read(w.fd, buf)
	if err != nil || nr == 0 {
		return false
	}
	// Drain events; any matching mask is interesting.
	offset := 0
	interesting := false
	for offset+unix.SizeofInotifyEvent <= nr {
		raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
		nameLen := int(raw.Len)
		mask := raw.Mask
		offset += unix.SizeofInotifyEvent + nameLen
		if mask&inQOverflow != 0 {
			interesting = true
			break
		}
		if mask&(inCloseWrite|inMovedTo|inMovedFrom|inDelete) != 0 {
			interesting = true
		}
	}
	return interesting
}

// Close releases the inotify fd.
func (w *InotifyWatcher) Close() {
	if w == nil || w.fd < 0 {
		return
	}
	_ = unix.Close(w.fd)
	w.fd = -1
	w.wdToPath = make(map[int]string)
}

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
		// Prefer absolute clean path.
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		out = append(out, path)
	}
	return out
}
