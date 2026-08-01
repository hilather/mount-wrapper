package mounter

// LimitReached reports whether active count has met or exceeded limit.
// limit <= 0 means unlimited (never reached).
//
// Used for max_concurrent_mount (0 = unlimited). max_concurrent_index is
// validated as >= 1 at config load; the same helper still applies.
func LimitReached(active, limit int) bool {
	if limit <= 0 {
		return false
	}
	return active >= limit
}

// SlotsAvailable reports how many more units can start under limit.
// Unlimited (limit <= 0) returns a large positive sentinel (-1 style avoided:
// returns a high number so callers can treat "any" as available).
func SlotsAvailable(active, limit int) int {
	if limit <= 0 {
		return 1_000_000_000
	}
	left := limit - active
	if left < 0 {
		return 0
	}
	return left
}

// CountActive is a tiny helper: count true flags (table-driven tests / fakes).
func CountActive(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}
