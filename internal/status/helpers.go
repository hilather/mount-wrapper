package status

import (
	"math"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/paths"
)

// Statuses considered "in progress" for indexing/mount progress section.
var inProgressStatuses = map[string]struct{}{
	"converting": {},
	"indexing":   {},
	"mounting":   {},
}

// Error statuses for errors_recent ranking.
var errorStatuses = map[string]struct{}{
	"index_failed": {},
	"mount_failed": {},
}

// countStatusKeys is the ordered set of statuses counted in the payload.
var countStatusKeys = []string{
	"discovered",
	"converting",
	"indexing",
	"index_failed",
	"mounting",
	"mount_failed",
	"mounted",
	"hooks_running",
	"unmounting",
	"absent",
}

// ElapsedSeconds returns seconds since startedISO, or nil if unparseable.
// now is Unix seconds; when zero and clock is non-nil, clock() is used.
// When both are zero/nil, time.Now is used.
func ElapsedSeconds(startedISO string, now float64, clock func() float64) *float64 {
	started := ParseISOToEpoch(startedISO)
	if started == nil {
		return nil
	}
	nowTS := now
	if nowTS == 0 {
		if clock != nil {
			nowTS = clock()
		} else {
			nowTS = float64(time.Now().UnixNano()) / 1e9
		}
	}
	elapsed := nowTS - *started
	if elapsed < 0 {
		elapsed = 0
	}
	return &elapsed
}

// Round1 rounds v to one decimal place (parity with Python round(x, 1)).
func Round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// SourceFSLabel returns "drvfs", "linux", or "unknown" for an archive path.
func SourceFSLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return "unknown"
	}
	if paths.IsDrvFsPath(path) {
		return "drvfs"
	}
	return "linux"
}

// ShouldLogIndexProgress reports whether enough time has passed since the last
// progress log (default interval 60s). lastLogAt nil → always true.
func ShouldLogIndexProgress(lastLogAt *float64, now, intervalS float64) bool {
	if lastLogAt == nil {
		return true
	}
	if intervalS <= 0 {
		intervalS = 60
	}
	return (now - *lastLogAt) >= intervalS
}

// ParseISOToEpoch parses ISO-8601 timestamps (with optional trailing Z) to Unix
// seconds. Returns nil if unparseable (parity with cleaner.parse_iso_utc /
// reconcile.parseISOToEpoch).
func ParseISOToEpoch(value string) *float64 {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000000Z",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, text); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
	}
	if strings.HasSuffix(text, "Z") {
		alt := strings.TrimSuffix(text, "Z") + "+00:00"
		if t, err := time.Parse(time.RFC3339Nano, alt); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
		if t, err := time.Parse(time.RFC3339, alt); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
	}
	return nil
}

func isInProgress(status string) bool {
	_, ok := inProgressStatuses[status]
	return ok
}

func isErrorStatus(status string) bool {
	_, ok := errorStatuses[status]
	return ok
}

func defaultPIDAlive(pid int) bool {
	return pid > 0
}

func pidAliveCheck(fn func(int) bool, mountPID *int64) bool {
	if mountPID == nil {
		return false
	}
	pid := int(*mountPID)
	if fn == nil {
		return defaultPIDAlive(pid)
	}
	return fn(pid)
}
