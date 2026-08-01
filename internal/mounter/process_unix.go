//go:build unix

package mounter

import (
	"os/exec"
	"syscall"
	"time"
)

// ApplyProcessGroup configures cmd to start in a new process group so the
// entire ratarmount tree can be signalled via killpg (parity with
// start_new_session=True / Setpgid).
func ApplyProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// IsProcessAlive reports whether pid looks alive (signal 0).
// Permission errors are treated as alive (exists but not owned by us).
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if err == syscall.EPERM {
		return true
	}
	return false
}

// KillProcessGroup sends sig to the process group led by pid.
// Falls back to signalling the single pid on group failures.
func KillProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return mounterErrorf("invalid pid %d", pid)
	}
	err := syscall.Kill(-pid, sig) // negative = process group
	if err == nil {
		return nil
	}
	return syscall.Kill(pid, sig)
}

// SignalProcess sends sig to a single pid.
func SignalProcess(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return mounterErrorf("invalid pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}

// WaitWithTimeout waits for cmd to exit up to timeout.
// On timeout, sends SIGTERM to the process group, waits a grace period, then SIGKILL.
//
// Only one Wait may be active on cmd; callers must not call Wait separately.
func WaitWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return normalizeWaitErr(err)
	case <-timer.C:
		pid := cmd.Process.Pid
		_ = KillProcessGroup(pid, syscall.SIGTERM)
		grace := 5 * time.Second
		if timeout < grace {
			grace = timeout
		}
		graceTimer := time.NewTimer(grace)
		defer graceTimer.Stop()
		select {
		case err := <-done:
			return normalizeWaitErr(err)
		case <-graceTimer.C:
			_ = KillProcessGroup(pid, syscall.SIGKILL)
			err := <-done
			return normalizeWaitErr(err)
		}
	}
}

// TerminateProcessGroup sends SIGTERM to the process group and waits up to
// wait (default 5s), then SIGKILL. Uses a single Wait on cmd when poll is nil.
//
// When poll is non-nil, Wait ownership stays with the poll (do not call
// cmd.Wait again — that races with ProcessPoll.StartWait).
func TerminateProcessGroup(cmd *exec.Cmd, wait time.Duration) {
	TerminateProcessGroupPoll(cmd, wait, nil)
}

// TerminateProcessGroupPoll is like TerminateProcessGroup but reaps via poll
// when provided (the Engine always attaches a ProcessPoll to live children).
//
// Do not read cmd.ProcessState here: Wait may still be writing it on another
// goroutine (ProcessPoll). Use poll.Done() or a dedicated Wait only.
func TerminateProcessGroupPoll(cmd *exec.Cmd, wait time.Duration, poll *ProcessPoll) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if wait <= 0 {
		wait = 5 * time.Second
	}
	pid := cmd.Process.Pid
	_ = KillProcessGroup(pid, syscall.SIGTERM)

	if poll != nil {
		poll.StartWait(cmd)
		done := poll.Done()
		if done == nil {
			// StartWait could not arm Wait (no process); nothing to reap.
			return
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			_ = KillProcessGroup(pid, syscall.SIGKILL)
			// Bound the final wait so a stuck poll cannot hang shutdown forever.
			final := time.NewTimer(wait)
			defer final.Stop()
			select {
			case <-done:
			case <-final.C:
			}
			return
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		_ = KillProcessGroup(pid, syscall.SIGKILL)
		<-done
	}
}

func normalizeWaitErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}
