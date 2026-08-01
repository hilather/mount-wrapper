package cleaner

import "strings"

// StripFdDeletedSuffix removes the Linux /proc fd " (deleted)" suffix that
// appears when a file is unlinked while still open.
func StripFdDeletedSuffix(link string) string {
	const suffix = " (deleted)"
	if strings.HasSuffix(link, suffix) {
		return strings.TrimSuffix(link, suffix)
	}
	return link
}

// FdLinkMatchesPath reports whether a /proc/*/fd readlink target refers to
// path or absPath (resolved). link may include a " (deleted)" suffix.
// Pure string match only — no filesystem resolve of the link target.
func FdLinkMatchesPath(link, path, absPath string) bool {
	base := StripFdDeletedSuffix(link)
	if base == "" {
		return false
	}
	if path != "" && base == path {
		return true
	}
	if absPath != "" && base == absPath {
		return true
	}
	return false
}
