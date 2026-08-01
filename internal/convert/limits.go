package convert

// LimitReached reports whether active convert count has met or exceeded limit.
// limit <= 0 means unlimited (never reached).
//
// Used with max_concurrent_convert. Config load validates >= 1 today; the pure
// helper still treats 0 as unlimited for callers that pass an override.
func LimitReached(active, limit int) bool {
	if limit <= 0 {
		return false
	}
	return active >= limit
}

// SlotsAvailable reports how many more convert jobs can start under limit.
// Unlimited (limit <= 0) returns a large positive sentinel.
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
