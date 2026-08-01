package platform

import (
	"io"
	"os"
	"os/exec"
)

// WhichFunc locates an executable by name (like exec.LookPath / shutil.which).
// Returns the resolved path, or empty string if not found.
type WhichFunc func(name string) string

// PathExistsFunc reports whether a path exists (for doctor probes / tests).
type PathExistsFunc func(path string) bool

// UnmountRunner runs an unmount argv and returns a process exit code.
// On failure to start the process, implementations should return a non-zero code
// (typically 1), matching upstream unmount_fuse.
type UnmountRunner func(argv []string) int

// FuseDeviceCandidates returns paths that indicate FUSE is available.
func FuseDeviceCandidates(platform string) []string {
	if platform == "" {
		platform = HostPlatform()
	}
	if HostPlatformOf(platform) == PlatformDarwin {
		// macFUSE historically exposes macfuse / osxfuse paths.
		return []string{
			"/Library/Filesystems/macfuse.fs",
			"/Library/Filesystems/osxfuse.fs",
			"/dev/macfuse0",
			"/dev/osxfuse0",
			"/dev/fuse",
		}
	}
	return []string{"/dev/fuse"}
}

// UnmountToolName is a human label for doctor / logs.
func UnmountToolName(platform string) string {
	if platform == "" {
		platform = HostPlatform()
	}
	if HostPlatformOf(platform) == PlatformDarwin {
		return "umount (macFUSE)"
	}
	return "fusermount3/fusermount"
}

// UnmountCommand returns argv to unmount mountPath, or nil if no tool is available.
//
// Linux: fusermount3 then fusermount with -u and optional -z (lazy); fallback
// umount with -l if lazy.
// Darwin: umount with optional -f (lazy); then diskutil unmount [force].
//
// which may be nil to use exec.LookPath.
func UnmountCommand(mountPath string, lazy bool, platform string, which WhichFunc) []string {
	if platform == "" {
		platform = HostPlatform()
	}
	if which == nil {
		which = lookPath
	}
	plat := HostPlatformOf(platform)

	if plat == PlatformDarwin {
		if umount := which("umount"); umount != "" {
			cmd := []string{umount}
			if lazy {
				// macOS has no fusermount -z; -f is the usual force unmount.
				cmd = append(cmd, "-f")
			}
			cmd = append(cmd, mountPath)
			return cmd
		}
		if diskutil := which("diskutil"); diskutil != "" {
			if lazy {
				return []string{diskutil, "unmount", "force", mountPath}
			}
			return []string{diskutil, "unmount", mountPath}
		}
		return nil
	}

	// Linux (and other Unix with fusermount)
	for _, binary := range []string{"fusermount3", "fusermount"} {
		if resolved := which(binary); resolved != "" {
			cmd := []string{resolved, "-u"}
			if lazy {
				cmd = append(cmd, "-z")
			}
			cmd = append(cmd, mountPath)
			return cmd
		}
	}
	if umount := which("umount"); umount != "" {
		cmd := []string{umount}
		if lazy {
			cmd = append(cmd, "-l") // Linux lazy unmount
		}
		cmd = append(cmd, mountPath)
		return cmd
	}
	return nil
}

// UnmountFuse runs the platform unmount tool.
// Returns the process exit code, or 127 if no tool is available.
// runner and which may be nil for real exec / LookPath.
func UnmountFuse(mountPath string, lazy bool, platform string, runner UnmountRunner, which WhichFunc) int {
	cmd := UnmountCommand(mountPath, lazy, platform, which)
	if cmd == nil {
		return 127
	}
	if runner == nil {
		runner = defaultUnmountRunner
	}
	return runner(cmd)
}

func lookPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func defaultUnmountRunner(argv []string) int {
	if len(argv) == 0 {
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// FuseProbe is the pure-data result of ProbeFusePresence (doctor formats it).
type FuseProbe struct {
	Platform   string   `json:"platform"`
	Candidates []string `json:"candidates"`
	Found      []string `json:"found"`
	OK         bool     `json:"ok"`
	Hint       string   `json:"hint"`
}

// ProbeFusePresence describes FUSE availability for doctor.
// pathExists may be nil to use os.Stat.
func ProbeFusePresence(platform string, pathExists PathExistsFunc) FuseProbe {
	if platform == "" {
		platform = HostPlatform()
	}
	plat := HostPlatformOf(platform)
	if pathExists == nil {
		pathExists = pathExistsDefault
	}
	candidates := FuseDeviceCandidates(plat)
	var found []string
	for _, p := range candidates {
		if pathExists(p) {
			found = append(found, p)
		}
	}
	ok := len(found) > 0
	hint := ""
	if !ok {
		switch plat {
		case PlatformDarwin:
			hint = "Install macFUSE (https://macfuse.github.io/) then re-run doctor"
		case PlatformLinux:
			hint = "Install fuse3 package if mounts fail"
		}
	}
	return FuseProbe{
		Platform:   plat,
		Candidates: candidates,
		Found:      found,
		OK:         ok,
		Hint:       hint,
	}
}

// UnmountProbe is the pure-data result of ProbeUnmountTool.
type UnmountProbe struct {
	Platform        string   `json:"platform"`
	Tool            string   `json:"tool"`
	CommandTemplate []string `json:"command_template"`
	OK              bool     `json:"ok"`
}

// ProbeUnmountTool describes unmount tool availability for doctor.
// which may be nil to use exec.LookPath.
func ProbeUnmountTool(platform string, which WhichFunc) UnmountProbe {
	if platform == "" {
		platform = HostPlatform()
	}
	plat := HostPlatformOf(platform)
	cmd := UnmountCommand("/tmp/fake-mount", false, plat, which)
	return UnmountProbe{
		Platform:        plat,
		Tool:            UnmountToolName(plat),
		CommandTemplate: cmd,
		OK:              cmd != nil,
	}
}

func pathExistsDefault(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
