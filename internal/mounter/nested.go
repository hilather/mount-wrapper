package mounter

import "regexp"

// Nested mount failure lines from ratarmount automount (minimal parity).
// Example:
//
//	Mounting of '/bad.7z' failed because of: corrupt data
var nestedMountFailRE = regexp.MustCompile(`Mounting of '([^']+)' failed because of: (.+)$`)

// NestedMountFailure is a parsed automount skip.
type NestedMountFailure struct {
	Path   string
	Reason string
}

// ParseNestedMountFailure returns a failure when line is a ratarmount automount skip.
func ParseNestedMountFailure(line string) *NestedMountFailure {
	m := nestedMountFailRE.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return &NestedMountFailure{Path: m[1], Reason: m[2]}
}
