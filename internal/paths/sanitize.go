package paths

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	unsafeMountChars = regexp.MustCompile(`[^A-Za-z0-9._+@-]+`)
	multiUnderscore  = regexp.MustCompile(`_+`)
)

// SanitizeMountName sanitizes an archive basename for use as a mount directory name.
//
// Rules (parity with tarmount-wsl):
//   - Replace characters outside [A-Za-z0-9._+@-] with _
//   - Collapse consecutive underscores
//   - Strip leading/trailing . and _
//   - Max 120 characters; empty → "archive"
//   - If name is already in taken, append --{archiveID[:8]}, then longer id
//     prefixes, then numeric discriminators
func SanitizeMountName(basename, archiveID string, taken map[string]struct{}) string {
	var name string
	if basename == "" {
		name = "archive"
	} else {
		name = unsafeMountChars.ReplaceAllString(basename, "_")
		name = multiUnderscore.ReplaceAllString(name, "_")
		name = strings.Trim(name, "._")
		if name == "" {
			name = "archive"
		}
		if len(name) > 120 {
			name = name[:120]
		}
	}

	if !isTaken(name, taken) {
		return name
	}

	id := archiveID
	if id == "" {
		id = "unknown"
	}

	suffix := id
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	candidate := collideCandidate(name, suffix)
	if !isTaken(candidate, taken) {
		return candidate
	}

	// Rare: more of the id, then numeric discriminator.
	for n := 8; n <= len(id); n++ {
		suffix = id[:n]
		candidate = collideCandidate(name, suffix)
		if !isTaken(candidate, taken) {
			return candidate
		}
	}

	// When archiveID is shorter than 8, the loop above may not have run
	// (range 8..len with len<8). Also cover double-collision on short ids
	// by always trying numeric extras after the initial [:8] attempt.
	// Re-match Python: for n in range(8, len(full_id)+1) — empty range if len<8.
	// Then numeric loop.
	i := 2
	for {
		prefix := id
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		extra := prefix + "-" + strconv.Itoa(i)
		candidate = collideCandidate(name, extra)
		if !isTaken(candidate, taken) {
			return candidate
		}
		i++
	}
}

func collideCandidate(name, suffix string) string {
	// base_max = 120 - len(suffix) - 2  ("--")
	baseMax := 120 - len(suffix) - 2
	if baseMax < 1 {
		baseMax = 1
	}
	base := name
	if len(base) > baseMax {
		base = base[:baseMax]
	}
	base = strings.TrimRight(base, "._")
	if base == "" {
		base = "archive"
	}
	return base + "--" + suffix
}

func isTaken(name string, taken map[string]struct{}) bool {
	if taken == nil {
		return false
	}
	_, ok := taken[name]
	return ok
}
