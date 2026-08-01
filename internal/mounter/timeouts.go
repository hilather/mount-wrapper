package mounter

import "time"

// DurationFromSeconds converts a config float seconds value to time.Duration.
// Non-positive values return the provided default (or 0 if default is also <= 0).
func DurationFromSeconds(seconds float64, defaultDur time.Duration) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if defaultDur > 0 {
		return defaultDur
	}
	return 0
}

// DefaultMountReadyTimeout matches config default mount_ready_timeout_seconds (24h).
const DefaultMountReadyTimeout = 86400 * time.Second

// DefaultUnmountTimeout matches config default unmount_timeout_seconds (60s).
const DefaultUnmountTimeout = 60 * time.Second

// MountReadyTimeout converts config.mount_ready_timeout_seconds.
func MountReadyTimeout(seconds float64) time.Duration {
	return DurationFromSeconds(seconds, DefaultMountReadyTimeout)
}

// UnmountTimeout converts config.unmount_timeout_seconds.
func UnmountTimeout(seconds float64) time.Duration {
	return DurationFromSeconds(seconds, DefaultUnmountTimeout)
}
