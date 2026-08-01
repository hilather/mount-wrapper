//go:build unix

package mounter

import "syscall"

func killOrphan(pid int) error {
	return SignalProcess(pid, syscall.SIGTERM)
}
