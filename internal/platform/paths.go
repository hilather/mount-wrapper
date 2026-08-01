package platform

import (
	"os"
	"path/filepath"
)

// DefaultPaths holds the default filesystem layout for a host profile.
// Keys mirror the config surface (mount_root, index_dir, …).
type DefaultPaths struct {
	MountRoot                 string
	IndexDir                  string
	OverlayDir                string
	StateDB                   string
	HooksDir                  string
	RatarmountBin             string
	ControlSocket             string
	PIDFile                   string
	ArchiveConverterOutputDir string
}

// LinuxDefaultPaths returns packaged Linux / WSL path defaults for mount-wrapper.
func LinuxDefaultPaths() DefaultPaths {
	const root = "/var/lib/mount-wrapper"
	const run = "/run/mount-wrapper"
	return DefaultPaths{
		MountRoot:                 filepath.Join(root, "mounts"),
		IndexDir:                  filepath.Join(root, "indexes"),
		OverlayDir:                filepath.Join(root, "overlays"),
		StateDB:                   filepath.Join(root, "state.db"),
		HooksDir:                  "/etc/mount-wrapper/hooks.d",
		RatarmountBin:             "ratarmount-rs",
		ControlSocket:             filepath.Join(run, "control.sock"),
		PIDFile:                   filepath.Join(run, "mount-wrapper.pid"),
		ArchiveConverterOutputDir: filepath.Join(root, "converted"),
	}
}

// DarwinDefaultPaths returns user-scoped macOS path defaults under home.
// Run dir (socket + pid) lives under Library/Caches; data under
// Library/Application Support. home should be an absolute home directory
// (e.g. from os.UserHomeDir); empty home falls back to os.UserHomeDir.
func DarwinDefaultPaths(home string) DefaultPaths {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	support := filepath.Join(home, "Library", "Application Support", "mount-wrapper")
	run := filepath.Join(home, "Library", "Caches", "mount-wrapper", "run")
	return DefaultPaths{
		MountRoot:                 filepath.Join(support, "mounts"),
		IndexDir:                  filepath.Join(support, "indexes"),
		OverlayDir:                filepath.Join(support, "overlays"),
		StateDB:                   filepath.Join(support, "state.db"),
		HooksDir:                  filepath.Join(support, "hooks.d"),
		RatarmountBin:             "ratarmount-rs",
		ControlSocket:             filepath.Join(run, "control.sock"),
		PIDFile:                   filepath.Join(run, "mount-wrapper.pid"),
		ArchiveConverterOutputDir: filepath.Join(support, "converted"),
	}
}

// DefaultPathsForHost returns default paths for the running host platform.
func DefaultPathsForHost() DefaultPaths {
	return DefaultPathsFor(HostPlatform())
}

// DefaultPathsFor returns default paths for an explicit platform label.
// Non-darwin platforms (including "other") use the Linux packaged layout.
func DefaultPathsFor(platform string) DefaultPaths {
	if HostPlatformOf(platform) == PlatformDarwin {
		return DarwinDefaultPaths("")
	}
	return LinuxDefaultPaths()
}

// DefaultWindowsVisible is the product default for windows_visible.
// false on darwin; true on linux (WSL primary target keeps historical default).
func DefaultWindowsVisible(platform string) bool {
	if platform == "" {
		platform = HostPlatform()
	}
	return HostPlatformOf(platform) != PlatformDarwin
}

// DefaultUseInotify is true only on linux (inotify). Darwin uses poll until
// FSEvents is added.
func DefaultUseInotify(platform string) bool {
	if platform == "" {
		platform = HostPlatform()
	}
	return HostPlatformOf(platform) == PlatformLinux
}
