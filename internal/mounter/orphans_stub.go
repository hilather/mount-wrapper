//go:build !unix

package mounter

import "github.com/hilather/mount-wrapper/internal/state"

// RatarmountProc describes a running ratarmount child.
type RatarmountProc struct {
	PID       int
	MountPath string
}

// OrphanMountReconcileResult summarizes boot-time orphan cleanup.
type OrphanMountReconcileResult struct {
	KilledPIDs []int
	Cleared    []string
}

var ratarmountProcLister = func() ([]RatarmountProc, error) { return nil, nil }

func FindRatarmountPIDsForMount(string) []int { return nil }

func ClearStaleMountHolders(string, int, UnmountOptions) ([]int, UnmountResult) {
	return nil, UnmountResult{}
}

func (e *Engine) ReconcileOrphanMounts([]*state.ArchiveRecord) OrphanMountReconcileResult {
	return OrphanMountReconcileResult{}
}

func (e *Engine) unmountOpts() UnmountOptions { return UnmountOptions{} }
