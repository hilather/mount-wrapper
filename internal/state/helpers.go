package state

import (
	"time"

	"github.com/google/uuid"
)

// UTCNowISO returns the current UTC time as an ISO-8601 string with a Z suffix.
// Microseconds are truncated (second precision), matching Python utc_now_iso().
func UTCNowISO() string {
	return time.Now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// NewArchiveID returns a new archive UUID4 string.
func NewArchiveID() string {
	return uuid.NewString()
}
