package mounter

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// PreparePaths creates mount, index parent, overlay, and log directories for req.
func PreparePaths(req MountRequest) error {
	if req.MountPath != "" {
		if err := os.MkdirAll(req.MountPath, 0o755); err != nil {
			return mounterErrorf("mkdir mount_path %s: %v", req.MountPath, err)
		}
	}
	if req.IndexPath != "" {
		if err := os.MkdirAll(filepath.Dir(req.IndexPath), 0o755); err != nil {
			return mounterErrorf("mkdir index parent %s: %v", filepath.Dir(req.IndexPath), err)
		}
	}
	if req.OverlayPath != "" {
		if err := os.MkdirAll(req.OverlayPath, 0o755); err != nil {
			return mounterErrorf("mkdir overlay_path %s: %v", req.OverlayPath, err)
		}
	}
	if dir := req.RatarmountLogDir; dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return mounterErrorf("mkdir ratarmount_log_dir %s: %v", dir, err)
		}
	}
	return nil
}

// CmdOptions customizes NewRatarmountCmd.
type CmdOptions struct {
	// Env is the child environment. Nil uses os.Environ() with no debug overrides.
	Env []string
	// Stdout defaults to io.Discard when nil.
	Stdout io.Writer
	// Stderr defaults to nil (inherit) when unset; set to a pipe writer for drain.
	Stderr io.Writer
	// Dir is the working directory (optional).
	Dir string
}

// NewRatarmountCmd builds an *exec.Cmd for req with process-group attributes.
// Does not start the process. Does not verify the archive exists.
func NewRatarmountCmd(req MountRequest, opts CmdOptions) *exec.Cmd {
	argv := BuildRatarmountCmd(req)
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	ApplyProcessGroup(cmd)
	return cmd
}

// StartProcess prepares paths and starts the ratarmount child.
// archiveMustExist, when true, fails if req.ArchivePath is not a regular file.
func StartProcess(req MountRequest, opts CmdOptions, archiveMustExist bool) (*exec.Cmd, error) {
	if archiveMustExist {
		info, err := os.Stat(req.ArchivePath)
		if err != nil || info.IsDir() {
			return nil, mounterErrorf("archive not found: %s", req.ArchivePath)
		}
	}
	if err := PreparePaths(req); err != nil {
		return nil, err
	}
	// Callers should run EnginePathsAllowed (DrvFs policy) before StartProcess.
	cmd := NewRatarmountCmd(req, opts)
	if cmd == nil {
		return nil, mounterErrorf("empty ratarmount command")
	}
	if err := cmd.Start(); err != nil {
		return nil, mounterErrorf("failed to spawn ratarmount: %v", err)
	}
	return cmd, nil
}
