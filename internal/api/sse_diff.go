package api

import (
	"encoding/json"
	"reflect"
	"sort"
)

// archiveWatchKeys are fields that trigger an archive SSE event when they change.
// Volatile bookkeeping (last_seen_at, generated timestamps) is intentionally omitted
// so routine scan ticks do not re-emit every row.
var archiveWatchKeys = []string{
	"status",
	"hooks_status",
	"progress_label",
	"elapsed_s",
	"last_error",
	"mount_path",
	"archive_path",
	"index_path",
	"overlay_path",
	"mount_pid",
	"pid_alive",
	"mount_attempts",
	"mount_retryable",
	"mount_phase",
	"is_first_index",
	"live_pid",
	"size_bytes",
	"is_mounted",
	"removed_at",
	"fingerprint",
	"metrics",
	"index_duration_seconds",
	"mount_duration_seconds",
	"convert_source_size_bytes",
	"convert_duration_seconds",
}

// topLevelCountKeys are snapshot fields mirrored into the counts event.
var topLevelCountKeys = []string{
	"mounted",
	"indexing",
	"mounting",
	"discovered",
	"hooks_running",
	"index_failed",
	"mount_failed",
	"absent",
}

// SnapshotDelta is the set of SSE payloads to emit between two status snapshots.
// Nil / empty fields mean "no event of this type".
type SnapshotDelta struct {
	// Counts is the counts event payload (when overview counts change).
	Counts map[string]any
	// LowDisk is emitted on low_disk edge (false→true or true→false).
	LowDisk map[string]any
	// Scan is emitted when last_scan_at changes.
	Scan map[string]any
	// Metrics is emitted when metrics_summary changes (include_sizes path).
	Metrics map[string]any
	// Archives is the list of new or changed archive dicts (full row maps).
	Archives []map[string]any
	// RemovedIDs lists archive_ids present in prev but absent in curr.
	RemovedIDs []string
}

// HasChanges reports whether any delta event should be sent.
func (d SnapshotDelta) HasChanges() bool {
	return d.Counts != nil ||
		d.LowDisk != nil ||
		d.Scan != nil ||
		d.Metrics != nil ||
		len(d.Archives) > 0 ||
		len(d.RemovedIDs) > 0
}

// ArchiveEventPayload builds the data object for an "archive" SSE event.
// Always uses a stable shape so SPA patch logic is simple:
//
//	{"archives":[...], "removed_ids":[...]}  (removed_ids omitted when empty)
func (d SnapshotDelta) ArchiveEventPayload() map[string]any {
	if len(d.Archives) == 0 && len(d.RemovedIDs) == 0 {
		return nil
	}
	out := map[string]any{
		"archives": d.Archives,
	}
	if d.Archives == nil {
		out["archives"] = []map[string]any{}
	}
	if len(d.RemovedIDs) > 0 {
		out["removed_ids"] = d.RemovedIDs
	}
	return out
}

// DiffSnapshots compares two status snapshot maps (as returned by snapshotPayload)
// and returns the delta events the SSE stream should emit.
//
// prev may be nil (treated as empty). When curr is nil or not ok, returns a zero delta.
func DiffSnapshots(prev, curr map[string]any) SnapshotDelta {
	var d SnapshotDelta
	if curr == nil {
		return d
	}
	if ok, has := curr["ok"].(bool); has && !ok {
		return d
	}
	if prev == nil {
		prev = map[string]any{}
	}

	// counts
	if !countsEqual(prev, curr) {
		d.Counts = buildCountsEvent(curr)
	}

	// low_disk edge
	prevLD := asBool(prev["low_disk"], false)
	currLD := asBool(curr["low_disk"], false)
	if prevLD != currLD {
		d.LowDisk = map[string]any{
			"low_disk": currLD,
		}
		if v, ok := curr["disk_free_bytes"]; ok {
			d.LowDisk["disk_free_bytes"] = v
		}
		if v, ok := curr["min_free_bytes"]; ok {
			d.LowDisk["min_free_bytes"] = v
		}
	}

	// scan finished / last_scan_at moved
	prevScanAt := asString(prev["last_scan_at"])
	currScanAt := asString(curr["last_scan_at"])
	if currScanAt != "" && currScanAt != prevScanAt {
		d.Scan = map[string]any{
			"last_scan_at": currScanAt,
		}
		if v, ok := curr["last_scan"]; ok {
			d.Scan["last_scan"] = v
		}
		if v, ok := curr["last_scan_duration_ms"]; ok {
			d.Scan["last_scan_duration_ms"] = v
		}
	}

	// metrics_summary (optional; only present with include_sizes)
	if !jsonEqual(prev["metrics_summary"], curr["metrics_summary"]) {
		if curr["metrics_summary"] != nil {
			d.Metrics = map[string]any{
				"metrics_summary": curr["metrics_summary"],
			}
		} else if prev["metrics_summary"] != nil {
			// Cleared — still notify so SPA can drop sizes.
			d.Metrics = map[string]any{
				"metrics_summary": nil,
			}
		}
	}

	// archives by id
	prevByID := archivesByID(prev["archives"])
	currByID := archivesByID(curr["archives"])

	var changedIDs []string
	for id, row := range currByID {
		old, existed := prevByID[id]
		if !existed || !archiveWatchEqual(old, row) {
			changedIDs = append(changedIDs, id)
		}
	}
	sort.Strings(changedIDs)
	for _, id := range changedIDs {
		d.Archives = append(d.Archives, currByID[id])
	}
	for id := range prevByID {
		if _, ok := currByID[id]; !ok {
			d.RemovedIDs = append(d.RemovedIDs, id)
		}
	}
	sort.Strings(d.RemovedIDs)
	return d
}

func buildCountsEvent(snap map[string]any) map[string]any {
	out := map[string]any{
		"counts":       snap["counts"],
		"low_disk":     snap["low_disk"],
		"last_scan_at": snap["last_scan_at"],
	}
	for _, k := range topLevelCountKeys {
		if v, ok := snap[k]; ok {
			out[k] = v
		}
	}
	return out
}

func countsEqual(prev, curr map[string]any) bool {
	if !jsonEqual(prev["counts"], curr["counts"]) {
		return false
	}
	for _, k := range topLevelCountKeys {
		if !jsonEqual(prev[k], curr[k]) {
			return false
		}
	}
	// low_disk / last_scan_at also ride on counts for SPA badges, but have
	// dedicated events; still re-emit counts when they flip so one handler works.
	if asBool(prev["low_disk"], false) != asBool(curr["low_disk"], false) {
		return false
	}
	if asString(prev["last_scan_at"]) != asString(curr["last_scan_at"]) {
		return false
	}
	return true
}

func archivesByID(v any) map[string]map[string]any {
	rows := asSliceOfMaps(v)
	out := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		id := asString(row["archive_id"])
		if id == "" {
			continue
		}
		out[id] = row
	}
	return out
}

func archiveWatchEqual(a, b map[string]any) bool {
	for _, k := range archiveWatchKeys {
		if !jsonEqual(a[k], b[k]) {
			return false
		}
	}
	return true
}

// jsonEqual compares two JSON-ish values after normalizing numbers/maps via
// round-trip when needed. reflect.DeepEqual covers most cases; numbers from
// JSON may be float64 vs json.Number so we fall back to marshaled equality.
func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if reflect.DeepEqual(a, b) {
		return true
	}
	// Normalize via JSON for float/int / map key order differences.
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}
