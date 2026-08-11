package mounter

// Test hooks (export_test.go pattern).

var TestParseRatarmountMountPath = parseRatarmountMountPath

func SetTestRatarmountProcLister(fn func() ([]RatarmountProc, error)) func() {
	prev := ratarmountProcLister
	ratarmountProcLister = fn
	return func() { ratarmountProcLister = prev }
}

func SetTestTerminateOrphanPID(fn func(int)) func() {
	prev := terminateOrphanPIDHook
	if fn == nil {
		terminateOrphanPIDHook = terminateOrphanPID
	} else {
		terminateOrphanPIDHook = fn
	}
	return func() { terminateOrphanPIDHook = prev }
}
