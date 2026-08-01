//go:build !unix

package mounter

func killOrphan(pid int) error {
	return mounterErrorf("cannot signal orphan pid %d on this platform", pid)
}
