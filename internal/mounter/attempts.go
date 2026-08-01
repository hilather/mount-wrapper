package mounter

// NextMountAttempt increments attempts after a mount/index failure and decides
// whether further retries are allowed (parity with mounter._mark_failed).
//
// retryable := attempts < maxMountAttempts (strict less-than after increment).
// maxMountAttempts should be >= 1 (config validates min 1).
func NextMountAttempt(currentAttempts, maxMountAttempts int) (attempts int, retryable bool) {
	attempts = currentAttempts + 1
	if maxMountAttempts < 1 {
		// Degenerate: never retryable once counted.
		return attempts, false
	}
	retryable = attempts < maxMountAttempts
	return attempts, retryable
}

// MountRetryable reports whether another attempt is allowed for the current
// attempt counter (without incrementing).
func MountRetryable(attempts, maxMountAttempts int) bool {
	if maxMountAttempts < 1 {
		return false
	}
	return attempts < maxMountAttempts
}

// ShouldResetAttempts is true for admin "retry" ops: clear attempts and mark retryable.
// Pure flag helper — state updates belong to the store/service layer.
func ShouldResetAttempts() (attempts int, retryable bool) {
	return 0, true
}
