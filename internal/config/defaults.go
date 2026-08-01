package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hilather/mount-wrapper/internal/platform"
)

// DefaultConfigPath is the system config path on Linux.
const DefaultConfigPath = "/etc/mount-wrapper/config.yaml"

// DefaultConfigPathDarwinSuffix is the relative path under $HOME for macOS.
const DefaultConfigPathDarwinSuffix = "Library/Application Support/mount-wrapper/config.yaml"

// Shipped default: tar family + zip only.
const DefaultNameRegex = `.*\.(tar(\.(gz|bz2|xz|zst))?|tgz|tbz2|txz|zip)$`

// DefaultRecursiveMountExtensions is the ratarmount --recursive-extensions list.
// Nested archives yes; plain gzip/raw logs no. /split is omitted (false positives
// on rotated logs like .log.1).
var DefaultRecursiveMountExtensions = []string{
	"/archive",
	"/disk",
	"/compressed/-",
	".raw/-",
	".log.gz/-",
}

// DefaultPaths holds Linux FHS path defaults for mount-wrapper.
// Note: ratarmount_bin is not applied when omitted from YAML; FromMap uses
// DefaultRatarmountBin so the default engine resolves to DefaultRustRatarmountBin.
var DefaultPaths = map[string]string{
	"mount_root":                  "/var/lib/mount-wrapper/mounts",
	"index_dir":                   "/var/lib/mount-wrapper/indexes",
	"overlay_dir":                 "/var/lib/mount-wrapper/overlays",
	"state_db":                    "/var/lib/mount-wrapper/state.db",
	"hooks_dir":                   "/etc/mount-wrapper/hooks.d",
	"ratarmount_bin":              DefaultRustRatarmountBin,
	"control_socket":              "/run/mount-wrapper/control.sock",
	"pid_file":                    "/run/mount-wrapper/mount-wrapper.pid",
	"archiveconverter_bin":        "", // filled by DefaultArchiveconverterBin()
	"archiveconverter_output_dir": "/var/lib/mount-wrapper/converted",
}

// PATH names; doctor/resolver may search PATH later. Only ratarmount-rs is supported.
const (
	DefaultRustRatarmountBin = "ratarmount-rs"
)

// DefaultArchiveconverterBin returns $HOME/.local/bin/archiveconverter, or
// "archiveconverter" when the home directory is unavailable.
func DefaultArchiveconverterBin() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "archiveconverter"
	}
	return filepath.Join(home, ".local", "bin", "archiveconverter")
}

// DefaultRatarmountBin returns the default engine binary name (always ratarmount-rs).
// The backend argument is accepted for API stability; only rust is valid.
func DefaultRatarmountBin(backend string) string {
	if _, err := NormalizeMountBackend(backend); err != nil && strings.TrimSpace(backend) != "" {
		// Invalid backend still returns the rust binary name for messaging.
		return DefaultRustRatarmountBin
	}
	return DefaultRustRatarmountBin
}

// DefaultConfigPathForHost returns the packaged default config path for the
// current OS: Linux FHS (/etc/…) or macOS user Application Support.
func DefaultConfigPathForHost() string {
	return DefaultConfigPathFor(runtime.GOOS)
}

// DefaultConfigPathFor returns the default config path for a GOOS-style label.
// Darwin uses ~/Library/Application Support/mount-wrapper/config.yaml;
// all other platforms use DefaultConfigPath (Linux FHS).
func DefaultConfigPathFor(goos string) string {
	if platform.HostPlatformOf(goos) == platform.PlatformDarwin {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return filepath.Join("/", DefaultConfigPathDarwinSuffix)
		}
		return filepath.Join(home, DefaultConfigPathDarwinSuffix)
	}
	return DefaultConfigPath
}

// ResolveConfigPath returns explicit if non-empty, else the host default
// (Linux /etc/… or Darwin Application Support).
func ResolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return DefaultConfigPathForHost()
}
