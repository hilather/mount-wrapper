package platform

import (
	"os"
	"runtime"
	"strings"
)

// Platform labels returned by HostPlatform / HostPlatformOf.
const (
	PlatformLinux  = "linux"
	PlatformDarwin = "darwin"
	PlatformOther  = "other"
)

// HostPlatform returns the host platform label for the running binary
// (linux, darwin, or other).
func HostPlatform() string {
	return HostPlatformOf(runtime.GOOS)
}

// HostPlatformOf maps a GOOS-style string to linux, darwin, or other.
// Any value with a "linux" prefix (e.g. "linux", "linux2") maps to linux.
func HostPlatformOf(goos string) string {
	p := strings.ToLower(strings.TrimSpace(goos))
	if strings.HasPrefix(p, "linux") {
		return PlatformLinux
	}
	if p == "darwin" {
		return PlatformDarwin
	}
	return PlatformOther
}

// IsLinux reports whether platform (or the host when empty) is linux.
func IsLinux(platform string) bool {
	if platform == "" {
		return HostPlatform() == PlatformLinux
	}
	return HostPlatformOf(platform) == PlatformLinux
}

// IsDarwin reports whether platform (or the host when empty) is darwin.
func IsDarwin(platform string) bool {
	if platform == "" {
		return HostPlatform() == PlatformDarwin
	}
	return HostPlatformOf(platform) == PlatformDarwin
}

// IsWSL is a best-effort WSL detector. It is only ever true on linux hosts.
// Checks WSL_DISTRO_NAME or WSL_INTEROP in the environment, then whether
// /proc/version contains "microsoft" or "wsl" (case-insensitive).
func IsWSL() bool {
	return isWSL(HostPlatform(), os.Getenv, readProcVersion)
}

// isWSL is the testable core of IsWSL.
func isWSL(platform string, getenv func(string) string, readVersion func() (string, error)) bool {
	if HostPlatformOf(platform) != PlatformLinux {
		return false
	}
	if getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != "" {
		return true
	}
	text, err := readVersion()
	if err != nil {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

func readProcVersion() (string, error) {
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
