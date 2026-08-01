package status

import (
	"fmt"
	"strings"
)

// FormatHuman returns a multi-line human-readable status (CLI non --json).
func FormatHuman(data *Payload) string {
	if data == nil {
		return "mount-wrapper ?  pid=?\n  (no archives tracked)\n"
	}
	var lines []string
	ver := data.Version
	if ver == "" {
		ver = "?"
	}
	lines = append(lines, fmt.Sprintf("mount-wrapper %s  pid=%d", ver, data.PID))
	if data.ConfigPath != "" {
		lines = append(lines, fmt.Sprintf("  config: %s", data.ConfigPath))
	}

	// Summary counts (use top-level convenience + converting/unmounting from Counts).
	type countKey struct {
		key string
		n   int
	}
	parts := make([]string, 0, 9)
	for _, ck := range []countKey{
		{"mounted", data.Mounted},
		{"converting", data.Counts["converting"]},
		{"indexing", data.Indexing},
		{"mounting", data.Mounting},
		{"discovered", data.Discovered},
		{"hooks_running", data.HooksRunning},
		{"index_failed", data.IndexFailed},
		{"mount_failed", data.MountFailed},
		{"absent", data.Absent},
	} {
		if ck.n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", ck.key, ck.n))
		}
	}
	if len(parts) > 0 {
		lines = append(lines, "  "+strings.Join(parts, "  "))
	} else {
		lines = append(lines, "  (no archives tracked)")
	}

	if data.LastScanAt != "" {
		durS := ""
		if data.LastScanDurationMs != nil {
			durS = fmt.Sprintf("  duration_ms=%.1f", *data.LastScanDurationMs)
		}
		lines = append(lines, fmt.Sprintf("  last_scan_at: %s%s", data.LastScanAt, durS))
	}

	if data.DiskFreeBytes != nil {
		free := *data.DiskFreeBytes
		gib := float64(free) / (1024 * 1024 * 1024)
		low := ""
		if data.LowDisk {
			low = "  LOW DISK"
		}
		lines = append(lines, fmt.Sprintf("  disk_free: %.2f GiB (%d bytes)%s", gib, free, low))
	}

	if len(data.IndexingArchives) > 0 {
		lines = append(lines, "  in progress:")
		for _, job := range data.IndexingArchives {
			el := "?"
			if job.ElapsedS != nil {
				el = fmt.Sprintf("%.0fs", *job.ElapsedS)
			}
			phase := job.ProgressLabel
			if phase == "" {
				phase = job.Status
			}
			var pid any
			if job.MountPID != nil {
				pid = *job.MountPID
			} else {
				pid = nil
			}
			lines = append(lines, fmt.Sprintf(
				"    [%s] %s  phase=%s  elapsed=%s  fs=%s  pid=%v",
				job.Status, job.Basename, phase, el, job.SourceFS, pid,
			))
		}
	}

	if len(data.ErrorsRecent) > 0 {
		lines = append(lines, "  errors / stuck:")
		limit := 10
		if len(data.ErrorsRecent) < limit {
			limit = len(data.ErrorsRecent)
		}
		for _, err := range data.ErrorsRecent[:limit] {
			stuck := ""
			if err.Stuck {
				stuck = " stuck"
			}
			msg := ""
			if err.LastError != nil {
				msg = *err.LastError
			}
			lines = append(lines, fmt.Sprintf(
				"    [%s]%s %s  attempts=%d  %s",
				err.Status, stuck, err.Basename, err.MountAttempts, msg,
			))
		}
	}

	if len(data.Archives) > 0 {
		lines = append(lines, "  archives:")
		for _, a := range data.Archives {
			extra := ""
			if isInProgress(a.Status) && a.ElapsedS != nil {
				phaseS := ""
				if a.ProgressLabel != "" {
					phaseS = "  " + a.ProgressLabel
				}
				extra = fmt.Sprintf("  elapsed=%.0fs%s", *a.ElapsedS, phaseS)
			}
			lines = append(lines, fmt.Sprintf(
				"    - %s [%s] hooks=%s%s  id=%s",
				a.ArchiveBasename, a.Status, a.HooksStatus, extra, a.ArchiveID,
			))
		}
	}

	return strings.Join(lines, "\n") + "\n"
}
