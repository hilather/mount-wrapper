package mounter

import "os/exec"

// lookPath is a thin wrapper so tests can stay free of real PATH when they
// inject WhichFunc; production uses exec.LookPath.
func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}
