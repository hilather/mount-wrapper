package convert

import "github.com/hilather/mount-wrapper/internal/state"

// ProgressLabelConverting is the SPA/status progress_label for convert jobs.
// Parity with tarmount-wsl status.py.
const ProgressLabelConverting = "converting to non-solid"

// ProgressLabel returns the progress label for a status string.
// Only "converting" has a convert-package label; indexing/mounting labels
// are owned by the status/mounter packages.
func ProgressLabel(status string) string {
	if status == state.StatusConverting {
		return ProgressLabelConverting
	}
	return ""
}

// CanEnterConverting reports whether a claim from fromStatus into converting
// is allowed by the state machine (ClaimConverting-friendly).
//
// ALLOWED_TRANSITIONS edges into converting:
//
//	discovered, mount_failed, unmounting
func CanEnterConverting(fromStatus string) bool {
	allowed, ok := state.ALLOWED_TRANSITIONS[fromStatus]
	if !ok {
		return false
	}
	_, ok = allowed[state.StatusConverting]
	return ok
}

// CanLeaveConverting reports whether leaving converting for toStatus is allowed.
func CanLeaveConverting(toStatus string) bool {
	allowed, ok := state.ALLOWED_TRANSITIONS[state.StatusConverting]
	if !ok {
		return false
	}
	_, ok = allowed[toStatus]
	return ok
}

// IsConvertingStatus reports whether status is the converting lifecycle state.
func IsConvertingStatus(status string) bool {
	return status == state.StatusConverting
}
