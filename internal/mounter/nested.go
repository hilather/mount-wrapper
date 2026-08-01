package mounter

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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
	m := nestedMountFailRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return nil
	}
	return &NestedMountFailure{Path: m[1], Reason: m[2]}
}

// DefaultNestedSkipSamples is how many nested paths appear in summaries.
const DefaultNestedSkipSamples = 3

// FormatNestedSkipSummary builds a short human summary of skipped nested mounts.
//
// Examples:
//
//	skipped 1 nested mount: /bad.7z
//	skipped 3 nested mounts: /a.7z, /b.7z, /c.7z
//	skipped 5 nested mounts: /a.7z, /b.7z, /c.7z (+2 more)
//
// Empty paths returns "". maxSamples <= 0 uses DefaultNestedSkipSamples.
func FormatNestedSkipSummary(paths []string, maxSamples int) string {
	if len(paths) == 0 {
		return ""
	}
	if maxSamples <= 0 {
		maxSamples = DefaultNestedSkipSamples
	}
	n := len(paths)
	unit := "nested mount"
	if n != 1 {
		unit = "nested mounts"
	}
	samples := paths
	more := 0
	if len(samples) > maxSamples {
		more = len(samples) - maxSamples
		samples = samples[:maxSamples]
	}
	msg := fmt.Sprintf("skipped %d %s: %s", n, unit, strings.Join(samples, ", "))
	if more > 0 {
		msg += fmt.Sprintf(" (+%d more)", more)
	}
	return msg
}

// EnrichReasonWithNestedSkips appends a nested-skip summary to reason when paths
// is non-empty. Used for last_error on MarkFailed (and optional diagnostics).
func EnrichReasonWithNestedSkips(reason string, paths []string) string {
	sum := FormatNestedSkipSummary(paths, DefaultNestedSkipSamples)
	if sum == "" {
		return reason
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return sum
	}
	return reason + "; " + sum
}

// PreserveNestedSkipInReason appends a nested-skip summary extracted from
// priorLastError onto reason when present. Used when overwriting last_error
// (hooks hard-fail / retry) so status/SPA can still derive nested_skips_* via
// ExtractNestedSkipSummary. Handles pure skip advisories and already-enriched
// failures ("reason; skipped N …"). Idempotent if reason already contains the
// summary segment.
func PreserveNestedSkipInReason(reason, priorLastError string) string {
	sum, n := ExtractNestedSkipSummary(priorLastError)
	if n <= 0 || sum == "" {
		return reason
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return sum
	}
	if strings.Contains(reason, sum) {
		return reason
	}
	return reason + "; " + sum
}

// nestedSkipSummaryRE matches FormatNestedSkipSummary output (optionally as a
// trailing "; skipped N …" segment of a richer last_error).
var nestedSkipSummaryRE = regexp.MustCompile(`^skipped (\d+) nested mounts?(?:: .+)?$`)

// ExtractNestedSkipSummary finds a nested-skip summary in last_error text.
// Returns the summary segment and parsed count, or ("", 0) when absent.
//
// Handles pure summaries ("skipped 2 nested mounts: /a, /b") and enriched
// failures ("engine exit 1; skipped 2 nested mounts: /a, /b").
func ExtractNestedSkipSummary(lastError string) (summary string, count int) {
	text := strings.TrimSpace(lastError)
	if text == "" {
		return "", 0
	}
	// Prefer the last "skipped … nested mount" segment (after optional "; ").
	idx := strings.LastIndex(text, "skipped ")
	if idx < 0 {
		return "", 0
	}
	candidate := strings.TrimSpace(text[idx:])
	m := nestedSkipSummaryRE.FindStringSubmatch(candidate)
	if m == nil {
		return "", 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return "", 0
	}
	return candidate, n
}

// IsNestedSkipOnlyLastError reports whether last_error is solely a nested-skip
// advisory (no failure reason prefix). Used so hooks success and MarkMounted
// do not wipe the operator-visible skip summary on mounted archives.
func IsNestedSkipOnlyLastError(lastError string) bool {
	text := strings.TrimSpace(lastError)
	if text == "" {
		return false
	}
	return nestedSkipSummaryRE.MatchString(text)
}

// DrainRatarmountStderr reads child stderr line-by-line until EOF, recording
// nested automount failures via note (path, reason). When logOther is true,
// non-matching non-empty lines are logged at info.
//
// Safe to call with note nil (parse-only / discard). Always drains until EOF
// so the child does not block on a full pipe buffer.
func DrainRatarmountStderr(r io.Reader, archiveID string, note func(path, reason string), logOther bool) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	// Nested paths + reasons can be long; raise beyond default 64KiB token.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if parsed := ParseNestedMountFailure(line); parsed != nil {
			if note != nil {
				note(parsed.Path, parsed.Reason)
			}
			slog.Info("nested archive skipped",
				"event", "nested_archive_skipped",
				"archive_id", archiveID,
				"path", parsed.Path,
				"reason", parsed.Reason,
			)
			continue
		}
		if logOther {
			text := strings.TrimRight(line, "\r\n")
			if text != "" {
				slog.Info("ratarmount stderr",
					"archive_id", archiveID,
					"line", text,
				)
			}
		}
	}
	if err := sc.Err(); err != nil {
		slog.Debug("ratarmount stderr drain ended",
			"archive_id", archiveID,
			"err", err,
		)
	}
}

// NoteNestedSkip records a nested automount path (thread-safe).
func (m *ManagedMount) NoteNestedSkip(path string) {
	if m == nil || path == "" {
		return
	}
	m.skipMu.Lock()
	defer m.skipMu.Unlock()
	m.SkippedNested = append(m.SkippedNested, path)
}

// NestedSkips returns a copy of skipped nested mount paths (thread-safe).
func (m *ManagedMount) NestedSkips() []string {
	if m == nil {
		return nil
	}
	m.skipMu.Lock()
	defer m.skipMu.Unlock()
	if len(m.SkippedNested) == 0 {
		return nil
	}
	out := make([]string, len(m.SkippedNested))
	copy(out, m.SkippedNested)
	return out
}

// WaitStderrDrain waits until the stderr drain goroutine finishes or timeout.
// No-op when no drain was started.
func (m *ManagedMount) WaitStderrDrain(timeout time.Duration) {
	if m == nil || m.stderrDone == nil {
		return
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	select {
	case <-m.stderrDone:
	case <-time.After(timeout):
	}
}

// LogNestedSkipSummary emits index_nested_skipped when any skips were recorded.
func LogNestedSkipSummary(archiveID string, paths []string) {
	if len(paths) == 0 {
		return
	}
	slog.Info("nested mounts skipped during index/mount",
		"event", "index_nested_skipped",
		"archive_id", archiveID,
		"count", len(paths),
		"summary", FormatNestedSkipSummary(paths, DefaultNestedSkipSamples),
	)
}

// shouldLogRatarmountStderr mirrors upstream: verbose when debug knobs are on.
func shouldLogRatarmountStderr(debug int, sevenZDebug bool, logDir, rustLog string) bool {
	return debug >= 2 || sevenZDebug || strings.TrimSpace(logDir) != "" || strings.TrimSpace(rustLog) != ""
}
