package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// PidFile holds an exclusive lock on the service pidfile.
// On Unix this uses flock LOCK_EX|LOCK_NB; on other platforms a best-effort
// open+write is used (see pidfile_*.go).
type PidFile struct {
	Path string
	file *os.File
}

// NewPidFile constructs a PidFile for path (not yet acquired).
func NewPidFile(path string) *PidFile {
	return &PidFile{Path: path}
}

// Acquire takes an exclusive non-blocking lock and writes the current PID.
func (p *PidFile) Acquire() error {
	if p == nil || p.Path == "" {
		return serviceErrorf("pidfile path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return serviceErrorf("create pidfile dir: %v", err)
	}
	f, err := os.OpenFile(p.Path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return serviceErrorf("open pidfile: %v", err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return serviceErrorf("another mount-wrapper serve instance holds %s", p.Path)
	}
	if err := f.Truncate(0); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return serviceErrorf("truncate pidfile: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return serviceErrorf("seek pidfile: %v", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return serviceErrorf("write pidfile: %v", err)
	}
	if err := f.Sync(); err != nil {
		// best-effort
		_ = err
	}
	p.file = f
	return nil
}

// Release unlocks and removes the pidfile.
func (p *PidFile) Release() {
	if p == nil || p.file == nil {
		return
	}
	_ = unlockFile(p.file)
	_ = p.file.Close()
	p.file = nil
	_ = os.Remove(p.Path)
}

// Held reports whether this PidFile currently holds the lock.
func (p *PidFile) Held() bool {
	return p != nil && p.file != nil
}
