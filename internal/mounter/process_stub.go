//go:build !unix

package mounter

import (
	"os/exec"
	"syscall"
	"time"
)

// ApplyProcessGroup is a no-op on non-Unix platforms.
func ApplyProcessGroup(cmd *exec.Cmd) {}

// IsProcessAlive is best-effort on non-Unix.
func IsProcessAlive(pid int) bool {
	return pid > 0
}

// KillProcessGroup is unsupported on non-Unix.
func KillProcessGroup(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return mounterErrorf("invalid pid %d", pid)
	}
	return mounterErrorf("process groups not supported on this platform")
}

// SignalProcess is unsupported on non-Unix.
func SignalProcess(pid int, _ syscall.Signal) error {
	return mounterErrorf("signals not supported on this platform")
}

// WaitWithTimeout waits for cmd or kills it after timeout.
func WaitWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if _, ok := err.(*exec.ExitError); ok {
			return nil
		}
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		err := <-done
		if _, ok := err.(*exec.ExitError); ok {
			return nil
		}
		return err
	}
}

// TerminateProcessGroup kills the process on non-Unix.
func TerminateProcessGroup(cmd *exec.Cmd, wait time.Duration) {
	TerminateProcessGroupPoll(cmd, wait, nil)
}

// TerminateProcessGroupPoll kills the process on non-Unix; poll is best-effort.
func TerminateProcessGroupPoll(cmd *exec.Cmd, wait time.Duration, poll *ProcessPoll) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	if poll != nil {
		poll.StartWait(cmd)
		if done := poll.Done(); done != nil {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				return
			}
		}
		return
	}
	_ = WaitWithTimeout(cmd, wait)
}
