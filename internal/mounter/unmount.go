package mounter

import (
	"os/exec"
	"time"

	"github.com/hilather/mount-wrapper/internal/platform"
)

// IsMountFunc reports whether path is a mountpoint (os.path.ismount parity).
type IsMountFunc func(path string) bool

// UnmountOptions controls UnmountSequence.
type UnmountOptions struct {
	// Timeout waiting for the mountpoint to clear after fusermount -u.
	Timeout time.Duration
	// Platform name for unmount tool selection ("" = host).
	Platform string
	// IsMount defaults to a stub that always returns false when nil.
	// Production should pass a real ismount (e.g. unix.IsMountpoint helper).
	IsMount IsMountFunc
	// Which locates fusermount/umount. Nil uses LookPath.
	Which platform.WhichFunc
	// Runner runs unmount argv. Nil uses real exec.
	Runner platform.UnmountRunner
	// Sleep between ismount polls. Nil uses time.Sleep.
	Sleep func(time.Duration)
	// Now for deadline. Nil uses time.Now.
	Now func() time.Time
	// Kill wait for process group after SIGTERM.
	KillWait time.Duration
	// WaitPoll owns cmd.Wait when the child is live-tracked (Engine). When set,
	// TerminateProcessGroup reaps via the poll instead of a second Wait.
	WaitPoll *ProcessPoll
}

// UnmountResult summarizes an unmount sequence.
type UnmountResult struct {
	KilledProcess bool
	UnmountCode   int  // last fusermount/umount exit code; 0 if not needed
	StillMounted  bool // true if mountpoint remained after lazy unmount
	LazyUsed      bool
}

// UnmountSequence implements: SIGTERM process group → wait → fusermount -u →
// poll until clear or timeout → lazy fusermount if still mounted.
//
// cmd may be nil when only a mount_path (and optional orphan pid) must be cleared.
// orphanPID, when > 0 and cmd is nil, receives SIGTERM if still alive.
func UnmountSequence(cmd *exec.Cmd, orphanPID int, mountPath string, opts UnmountOptions) UnmountResult {
	res := UnmountResult{}
	isMount := opts.IsMount
	if isMount == nil {
		isMount = func(string) bool { return false }
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultUnmountTimeout
	}
	killWait := opts.KillWait
	if killWait <= 0 {
		killWait = 5 * time.Second
	}

	if cmd != nil && cmd.Process != nil {
		TerminateProcessGroupPoll(cmd, killWait, opts.WaitPoll)
		res.KilledProcess = true
	} else if orphanPID > 0 && IsProcessAlive(orphanPID) {
		_ = killOrphan(orphanPID)
		res.KilledProcess = true
	}

	if mountPath == "" || !isMount(mountPath) {
		return res
	}

	res.UnmountCode = platform.UnmountFuse(mountPath, false, opts.Platform, opts.Runner, opts.Which)
	deadline := now().Add(timeout)
	for now().Before(deadline) && isMount(mountPath) {
		sleep(100 * time.Millisecond)
	}
	if isMount(mountPath) {
		res.LazyUsed = true
		res.UnmountCode = platform.UnmountFuse(mountPath, true, opts.Platform, opts.Runner, opts.Which)
		// Brief final poll
		end := now().Add(timeout)
		for now().Before(end) && isMount(mountPath) {
			sleep(100 * time.Millisecond)
		}
	}
	res.StillMounted = isMount(mountPath)
	return res
}

// FusermountUnmount is a thin adapter over platform.UnmountFuse.
func FusermountUnmount(mountPath string, lazy bool, plat string, runner platform.UnmountRunner, which platform.WhichFunc) int {
	return platform.UnmountFuse(mountPath, lazy, plat, runner, which)
}
