package match

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// MatchError is returned for invalid match configuration (e.g. bad regex).
type MatchError struct {
	Message string
}

func (e *MatchError) Error() string {
	if e == nil {
		return "match error"
	}
	return e.Message
}

func matchErrorf(format string, args ...any) *MatchError {
	return &MatchError{Message: fmt.Sprintf(format, args...)}
}

// CompileNameRegex compiles pattern for archive basename matching.
// An empty pattern uses config.DefaultNameRegex.
func CompileNameRegex(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		pattern = config.DefaultNameRegex
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, matchErrorf("invalid name_regex: %v", err)
	}
	return re, nil
}

// NormalizeExtensions normalizes extension allow-list entries to lowercase
// with a leading dot. Accepts "zip", ".ZIP", "tar.gz" → ".zip", ".zip", ".tar.gz".
// Empty strings are skipped. Duplicates are removed while preserving order.
func NormalizeExtensions(extensions []string) []string {
	if len(extensions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(extensions))
	result := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		cleaned := strings.ToLower(strings.TrimSpace(ext))
		if cleaned == "" {
			continue
		}
		if !strings.HasPrefix(cleaned, ".") {
			cleaned = "." + cleaned
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// basename returns the final path component, accepting both Windows and POSIX
// separators (parity with Python PurePath after backslash→slash normalize).
func basename(name string) string {
	s := strings.ReplaceAll(name, "\\", "/")
	return path.Base(s)
}

// ExtensionAllowed reports whether name's suffix is in extensions, or true
// when extensions is empty/nil (no allow-list filter).
// Matching is case-insensitive. Multi-part suffixes like ".tar.gz" are
// recognized when the allow-list contains ".tar.gz" (not only the last ".gz").
func ExtensionAllowed(name string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	base := strings.ToLower(basename(name))
	allowed := NormalizeExtensions(extensions)
	if len(allowed) == 0 {
		return true
	}
	for _, ext := range allowed {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// MatchesArchiveName reports whether the archive name (basename) should be managed.
//
// Filters (AND):
//  1. Basename matches nameRegex (empty → config.DefaultNameRegex).
//  2. If extensions is non-empty, basename ends with one of those extensions.
//
// The regex is applied to the basename only (not the full path). Use "(?i)" in
// the pattern for case-insensitive matching when needed.
func MatchesArchiveName(name, nameRegex string, extensions []string) (bool, error) {
	base := basename(name)
	// path.Base("") is "."; reject empty/dot/dotdot basenames.
	if name == "" || base == "" || base == "." || base == ".." {
		return false, nil
	}
	if !ExtensionAllowed(base, extensions) {
		return false, nil
	}
	re, err := CompileNameRegex(nameRegex)
	if err != nil {
		return false, err
	}
	return re.MatchString(base), nil
}

// FilterArchiveNames returns basenames from names that pass MatchesArchiveName,
// in input order. Output values are always basenames (not full paths).
func FilterArchiveNames(names []string, nameRegex string, extensions []string) ([]string, error) {
	re, err := CompileNameRegex(nameRegex)
	if err != nil {
		return nil, err
	}
	exts := NormalizeExtensions(extensions)
	result := make([]string, 0)
	for _, name := range names {
		if name == "" {
			continue
		}
		base := basename(name)
		if base == "" || base == "." || base == ".." {
			continue
		}
		if !ExtensionAllowed(base, exts) {
			continue
		}
		if !re.MatchString(base) {
			continue
		}
		result = append(result, base)
	}
	return result, nil
}
