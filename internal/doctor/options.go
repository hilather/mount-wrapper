package doctor

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/platform"
)

// WhichFunc locates an executable by name (like exec.LookPath / shutil.which).
// Returns the resolved path, or empty string if not found.
type WhichFunc func(name string) string

// PathExistsFunc reports whether a path exists.
type PathExistsFunc func(path string) bool

// IsExecutableFunc reports whether path is an existing executable file.
type IsExecutableFunc func(path string) bool

// IsDirFunc reports whether path exists and is a directory.
type IsDirFunc func(path string) bool

// WritableFunc reports whether path is writable by the current process.
type WritableFunc func(path string) bool

// FreeBytesFunc returns free bytes on the filesystem containing path.
// ok is false when the probe fails.
type FreeBytesFunc func(path string) (free int64, ok bool)

// LookupUserFunc looks up a system user by name. Returns false if missing.
type LookupUserFunc func(name string) (exists bool)

// ReadFileFunc reads a whole file (for fuse.conf, /proc/1/comm).
type ReadFileFunc func(path string) (string, error)

// RunBinFunc runs bin with args and returns combined output (for --version / --help).
// Implementations should apply a short timeout. err non-nil means probe failed.
type RunBinFunc func(bin string, args ...string) (output string, err error)

// WriteFileFunc writes content to path with mode (for --fix-systemd).
type WriteFileFunc func(path string, content []byte, mode os.FileMode) error

// MkdirAllFunc creates directories (for --fix-systemd parent dir).
type MkdirAllFunc func(path string, mode os.FileMode) error

// Service user / group defaults (D9).
const (
	DefaultServiceUser  = "mount-wrapper"
	DefaultServiceGroup = "mount-wrapper"
)

// Systemd drop-in paths for mount-wrapper.service.
const (
	DefaultSystemdDropinDir  = "/etc/systemd/system/mount-wrapper.service.d"
	DefaultSystemdDropinFile = DefaultSystemdDropinDir + "/sources.conf"
)

// DefaultFuseConfPath is the Linux FUSE config path.
const DefaultFuseConfPath = "/etc/fuse.conf"

// Options customizes Run (injectable for tests).
//
// Nil function fields use production defaults. Platform empty uses
// platform.HostPlatform(). Config may be nil for host/binary-only checks.
type Options struct {
	// Config is a validated config (optional). Path/source/binary checks use it.
	Config *config.Config

	// Platform overrides host platform (linux | darwin | other).
	Platform string

	// FixSystemd writes a systemd drop-in for source/stage paths when true.
	FixSystemd bool
	// DropinPath overrides DefaultSystemdDropinFile (tests).
	DropinPath string

	// ServiceUser is the expected dedicated user (default mount-wrapper).
	ServiceUser string

	// FuseConfPath defaults to /etc/fuse.conf.
	FuseConfPath string

	// MinFreeWarnBytes triggers a low-disk warn when free space is below this
	// and Config.MinFreeBytes is zero. Zero disables the synthetic threshold
	// (Config.MinFreeBytes still applies when set).
	// When both are zero, free-space checks still report measured free if available.
	MinFreeWarnBytes int64

	// GoVersion overrides runtime.Version() (tests).
	GoVersion string

	// IsWSL forces WSL detection when non-nil (tests).
	IsWSL *bool

	// Injectable probes (nil = production defaults).
	Which        WhichFunc
	PathExists   PathExistsFunc
	IsExecutable IsExecutableFunc
	IsDir        IsDirFunc
	Writable     WritableFunc
	FreeBytes    FreeBytesFunc
	LookupUser   LookupUserFunc
	ReadFile     ReadFileFunc
	RunBin       RunBinFunc
	WriteFile    WriteFileFunc
	MkdirAll     MkdirAllFunc
	ReadPID1Comm func() (string, error)
}

func (o *Options) platform() string {
	if o != nil && o.Platform != "" {
		return platform.HostPlatformOf(o.Platform)
	}
	return platform.HostPlatform()
}

func (o *Options) which() WhichFunc {
	if o != nil && o.Which != nil {
		return o.Which
	}
	return defaultWhich
}

func (o *Options) pathExists() PathExistsFunc {
	if o != nil && o.PathExists != nil {
		return o.PathExists
	}
	return defaultPathExists
}

func (o *Options) isExecutable() IsExecutableFunc {
	if o != nil && o.IsExecutable != nil {
		return o.IsExecutable
	}
	return defaultIsExecutable
}

func (o *Options) isDir() IsDirFunc {
	if o != nil && o.IsDir != nil {
		return o.IsDir
	}
	return defaultIsDir
}

func (o *Options) writable() WritableFunc {
	if o != nil && o.Writable != nil {
		return o.Writable
	}
	return defaultWritable
}

func (o *Options) freeBytes() FreeBytesFunc {
	if o != nil && o.FreeBytes != nil {
		return o.FreeBytes
	}
	return diskFreeBytes
}

func (o *Options) lookupUser() LookupUserFunc {
	if o != nil && o.LookupUser != nil {
		return o.LookupUser
	}
	return defaultLookupUser
}

func (o *Options) readFile() ReadFileFunc {
	if o != nil && o.ReadFile != nil {
		return o.ReadFile
	}
	return defaultReadFile
}

func (o *Options) writeFile() WriteFileFunc {
	if o != nil && o.WriteFile != nil {
		return o.WriteFile
	}
	return defaultWriteFile
}

func (o *Options) mkdirAll() MkdirAllFunc {
	if o != nil && o.MkdirAll != nil {
		return o.MkdirAll
	}
	return os.MkdirAll
}

func (o *Options) readPID1() func() (string, error) {
	if o != nil && o.ReadPID1Comm != nil {
		return o.ReadPID1Comm
	}
	return defaultReadPID1Comm
}

func (o *Options) fuseConfPath() string {
	if o != nil && o.FuseConfPath != "" {
		return o.FuseConfPath
	}
	return DefaultFuseConfPath
}

func (o *Options) serviceUser() string {
	if o != nil && o.ServiceUser != "" {
		return o.ServiceUser
	}
	return DefaultServiceUser
}

func (o *Options) goVersion() string {
	if o != nil && o.GoVersion != "" {
		return o.GoVersion
	}
	return runtime.Version()
}

func (o *Options) isWSL() bool {
	if o != nil && o.IsWSL != nil {
		return *o.IsWSL
	}
	return platform.IsWSL()
}

func (o *Options) dropinPath() string {
	if o != nil && o.DropinPath != "" {
		return o.DropinPath
	}
	return DefaultSystemdDropinFile
}

func defaultWhich(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func defaultPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultIsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func defaultIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func defaultWritable(path string) bool {
	// os.Access is not portable; try open for write via temporary create when dir.
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		f, err := os.CreateTemp(path, ".mw-doctor-write-*")
		if err != nil {
			return false
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return true
	}
	// File: open RDWR
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func defaultLookupUser(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

func defaultReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// runBinWithTimeout bounds --version / --help probes so a hung binary cannot
// stall doctor forever. Tests inject Options.RunBin and never hit this.
func runBinWithTimeout(bin string, timeout time.Duration, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), os.ErrDeadlineExceeded
	}
}

func defaultWriteFile(path string, content []byte, mode os.FileMode) error {
	return os.WriteFile(path, content, mode)
}

func defaultReadPID1Comm() (string, error) {
	b, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// resolveParent returns path if it exists, else its parent directory.
func resolveParent(path string, exists PathExistsFunc) string {
	if exists(path) {
		return path
	}
	return filepath.Dir(path)
}
