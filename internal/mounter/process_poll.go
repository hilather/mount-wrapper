package mounter

import (
	"os/exec"
	"sync"
)

// ProcessPoll tracks non-blocking Wait state for a child process.
// Safe for concurrent Poll after StartWait; only one Wait is issued.
// Callers that need to reap the child (including terminate) must use this
// poll instead of calling cmd.Wait a second time.
type ProcessPoll struct {
	once     sync.Once
	done     chan struct{}
	exitCode int
	exited   bool
}

// StartWait begins a background Wait on cmd (idempotent).
func (p *ProcessPoll) StartWait(cmd *exec.Cmd) {
	if p == nil || cmd == nil || cmd.Process == nil {
		return
	}
	p.once.Do(func() {
		p.done = make(chan struct{})
		go func() {
			_ = cmd.Wait()
			if cmd.ProcessState != nil {
				p.exitCode = cmd.ProcessState.ExitCode()
			}
			p.exited = true
			close(p.done)
		}()
	})
}

// Done returns a channel closed when the background Wait finishes.
// Returns a closed channel when p is nil. When Wait has not been started,
// returns nil (select on nil blocks forever — callers must StartWait first).
func (p *ProcessPoll) Done() <-chan struct{} {
	if p == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return p.done
}

// Poll reports whether the process has exited and its exit code.
// Starts Wait if not already started. Uses only ProcessPoll fields after Wait
// (does not race on cmd.ProcessState with the Wait goroutine).
func (p *ProcessPoll) Poll(cmd *exec.Cmd) (exited bool, exitCode *int) {
	if p == nil {
		return true, nil
	}
	p.StartWait(cmd)
	if p.done == nil {
		// No process to wait on.
		if cmd == nil || cmd.Process == nil {
			return true, nil
		}
		return false, nil
	}
	select {
	case <-p.done:
		c := p.exitCode
		return true, &c
	default:
		return false, nil
	}
}
