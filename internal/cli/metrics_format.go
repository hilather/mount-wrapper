package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// formatMetricsHuman renders a control "metrics" data payload for non-JSON CLI.
//
// Expected shapes (control ok data):
//
//	{"metrics": {…one ArchiveMetrics…}}                 // archive_id query
//	{"metrics": […], "summary": {…Summary…}}           // all archives
//
// Output is multi-line: optional summary block, then per-archive sizes.
func formatMetricsHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "metrics: (empty)\n"
	}
	var b strings.Builder
	b.WriteString("mount-wrapper metrics\n")

	if sum, ok := m["summary"].(map[string]any); ok && sum != nil {
		b.WriteString(formatMetricsSummaryBlock(sum))
	}

	switch raw := m["metrics"].(type) {
	case map[string]any:
		// Single-archive response (no summary from control).
		writeArchiveMetricsLine(&b, raw, indentMetricsTop)
	case []any:
		if len(raw) == 0 {
			if _, hasSum := m["summary"]; !hasSum {
				b.WriteString("  (no archives)\n")
			} else {
				b.WriteString("  archives: (none)\n")
			}
			return b.String()
		}
		b.WriteString("  archives:\n")
		// Stable order: basename then archive_id.
		rows := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			if row, ok := item.(map[string]any); ok && row != nil {
				rows = append(rows, row)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			bi := anyString(rows[i]["archive_basename"], anyString(rows[i]["archive_path"], ""))
			bj := anyString(rows[j]["archive_basename"], anyString(rows[j]["archive_path"], ""))
			if bi != bj {
				return bi < bj
			}
			return anyString(rows[i]["archive_id"], "") < anyString(rows[j]["archive_id"], "")
		})
		for _, row := range rows {
			writeArchiveMetricsLine(&b, row, indentMetricsList)
		}
	case nil:
		if _, hasSum := m["summary"]; !hasSum {
			b.WriteString("  (no metrics)\n")
		}
	default:
		b.WriteString("  (unrecognized metrics payload)\n")
	}
	return b.String()
}

// formatStatusSizesAppendix renders metrics_summary + per-archive metrics from a
// status payload (include_sizes). Empty string when no size data is present.
func formatStatusSizesAppendix(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return ""
	}
	return formatStatusSizesAppendixMap(m)
}

func formatStatusSizesAppendixMap(m map[string]any) string {
	sum, hasSum := m["metrics_summary"].(map[string]any)
	archives := anySlice(m["archives"])
	hasRow := false
	for _, raw := range archives {
		a, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := a["metrics"].(map[string]any); ok {
			hasRow = true
			break
		}
	}
	if !hasSum && !hasRow {
		return ""
	}

	var b strings.Builder
	b.WriteString("  sizes:\n")
	if hasSum && sum != nil {
		// Indent summary one level under "sizes:".
		block := formatMetricsSummaryBlock(sum)
		for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
			if line == "" {
				continue
			}
			// formatMetricsSummaryBlock lines already start with two spaces.
			b.WriteString("  " + line + "\n")
		}
	}
	if hasRow {
		b.WriteString("    per-archive:\n")
		for _, raw := range archives {
			a, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			met, ok := a["metrics"].(map[string]any)
			if !ok || met == nil {
				continue
			}
			// Copy identity from status row when metrics object omits it.
			row := met
			if anyString(row["archive_id"], "") == "" || anyString(row["archive_basename"], "") == "" ||
				anyString(row["status"], "") == "" {
				row = cloneMap(met)
				if anyString(row["archive_id"], "") == "" {
					row["archive_id"] = a["archive_id"]
				}
				if anyString(row["archive_basename"], "") == "" {
					row["archive_basename"] = a["archive_basename"]
				}
				if anyString(row["archive_path"], "") == "" {
					row["archive_path"] = a["archive_path"]
				}
				if anyString(row["status"], "") == "" {
					row["status"] = a["status"]
				}
			}
			writeArchiveMetricsLine(&b, row, indentStatusPerArchive)
		}
	}
	return b.String()
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// formatMetricsSummaryBlock prints summary totals (two-space indent).
func formatMetricsSummaryBlock(sum map[string]any) string {
	var b strings.Builder
	n := anyInt(sum["archive_count"])
	withExt := anyInt(sum["archives_with_extracted_size"])
	withConv := anyInt(sum["archives_with_convert_metadata"])
	fmt.Fprintf(&b, "  summary: archives=%d  with_extracted=%d  with_convert=%d\n", n, withExt, withConv)
	fmt.Fprintf(&b, "    archive total:   %s\n", formatBytes(anyInt64(sum["total_archive_size_bytes"])))
	fmt.Fprintf(&b, "    index total:     %s\n", formatBytes(anyInt64(sum["total_index_size_bytes"])))
	fmt.Fprintf(&b, "    extracted total: %s\n", formatBytes(anyInt64(sum["total_extracted_size_bytes"])))
	fmt.Fprintf(&b, "    space saved:     %s\n", formatBytes(anyInt64(sum["total_space_saved_bytes"])))
	withRSS := anyInt(sum["archives_with_mount_rss"])
	if withRSS > 0 || anyInt64(sum["total_mount_rss_bytes"]) > 0 {
		fmt.Fprintf(&b, "    mount RSS total: %s  (n=%d)\n",
			formatBytes(anyInt64(sum["total_mount_rss_bytes"])), withRSS)
		if peak := anyInt64(sum["total_mount_rss_peak_bytes"]); peak > 0 {
			fmt.Fprintf(&b, "    mount RSS peak:  %s\n", formatBytes(peak))
		}
	}

	if v, ok := sum["total_convert_source_size_bytes"]; ok && v != nil {
		fmt.Fprintf(&b, "    convert source:  %s\n", formatBytes(anyInt64(v)))
	}
	if v, ok := sum["total_convert_size_delta_bytes"]; ok && v != nil {
		fmt.Fprintf(&b, "    convert delta:   %s\n", formatSignedBytes(anyInt64(v)))
	}
	if v, ok := sum["max_convert_duration_seconds"]; ok && v != nil {
		nDur := anyInt(sum["archives_with_convert_duration"])
		fmt.Fprintf(&b, "    convert duration max: %s", formatDurationCell(anyFloat64(v)))
		if nDur > 0 {
			fmt.Fprintf(&b, " (n=%d)", nDur)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Indent levels for writeArchiveMetricsLine.
const (
	indentMetricsTop       = 1 // under "mount-wrapper metrics" (single-archive)
	indentMetricsList      = 2 // under "  archives:"
	indentStatusPerArchive = 3 // under status "    per-archive:"
)

// writeArchiveMetricsLine appends one archive metrics block at the given indent level.
func writeArchiveMetricsLine(b *strings.Builder, row map[string]any, level int) {
	indent := strings.Repeat("  ", level)
	detail := strings.Repeat("  ", level+1)
	id := anyString(row["archive_id"], "?")
	st := anyString(row["status"], "?")
	base := anyString(row["archive_basename"], "")
	if base == "" {
		base = anyString(row["archive_path"], id)
	}
	fmt.Fprintf(b, "%s[%s] %s  %s\n", indent, st, shortID(id), base)

	arch := formatBytesNullable(row["archive_size_bytes"])
	idx := formatBytesNullable(row["index_size_bytes"])
	ext := formatBytesNullable(row["extracted_size_bytes"])
	src := anyString(row["extracted_source"], "")
	nesting := anyString(row["extracted_nesting"], "")
	extNote := ""
	switch {
	case src != "" && nesting != "":
		extNote = " (" + src + ", " + nesting + ")"
	case src != "":
		extNote = " (" + src + ")"
	case nesting != "":
		extNote = " (" + nesting + ")"
	}
	fmt.Fprintf(b, "%sarchive=%s  index=%s  extracted=%s%s\n", detail, arch, idx, ext, extNote)
	if opaqueN, ok := row["opaque_nested_count"]; ok && opaqueN != nil {
		if n := anyInt64(opaqueN); n > 0 {
			fmt.Fprintf(b, "%sopaque_nested=%d (%s)\n", detail, n, formatBytesNullable(row["opaque_nested_bytes"]))
		}
	}

	saved := formatBytesNullable(row["space_saved_bytes"])
	vsArch := formatBytesNullable(row["space_saved_vs_archive_bytes"])
	fmt.Fprintf(b, "%sspace_saved=%s  vs_archive=%s\n", detail, saved, vsArch)
	if rss := formatBytesNullable(row["mount_rss_bytes"]); rss != "—" {
		peak := formatBytesNullable(row["mount_rss_peak_bytes"])
		pid := anyInt64(row["mount_pid"])
		if peak != "—" {
			fmt.Fprintf(b, "%smount_rss=%s  peak=%s  pid=%d\n", detail, rss, peak, pid)
		} else {
			fmt.Fprintf(b, "%smount_rss=%s  pid=%d\n", detail, rss, pid)
		}
	}

	var convParts []string
	if v, ok := row["convert_source_size_bytes"]; ok && v != nil {
		convParts = append(convParts, "source="+formatBytesNullable(v))
	}
	if v, ok := row["convert_size_delta_bytes"]; ok && v != nil {
		convParts = append(convParts, "delta="+formatSignedBytesNullable(v))
	}
	if v, ok := row["convert_duration_seconds"]; ok && v != nil {
		convParts = append(convParts, "duration="+formatDurationCell(anyFloat64(v)))
	}
	if len(convParts) > 0 {
		fmt.Fprintf(b, "%sconvert %s\n", detail, strings.Join(convParts, "  "))
	}
	if errMsg := anyString(row["error"], ""); errMsg != "" {
		fmt.Fprintf(b, "%serr=%s\n", detail, errMsg)
	}
}

// formatBytes formats a non-negative byte count (IEC KiB/MiB/GiB), matching SPA style.
// Negative values render as "—" (use formatSignedBytes for convert deltas).
func formatBytes(n int64) string {
	if n < 0 {
		return "—"
	}
	// Match web/src/lib/format.ts: units include B; divide while x >= 1024.
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	x := float64(n)
	i := 0
	for x >= 1024 && i < len(units)-1 {
		x /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	// digits: >=100 → 0, >=10 → 1, else 2.
	digits := 2
	if x >= 100 {
		digits = 0
	} else if x >= 10 {
		digits = 1
	}
	return fmt.Sprintf("%.*f %s", digits, x, units[i])
}

// formatSignedBytes formats a signed byte delta (+/- prefix), matching SPA convert delta.
func formatSignedBytes(n int64) string {
	if n == 0 {
		return "0 B"
	}
	abs := n
	if abs < 0 {
		abs = -abs
	}
	text := formatBytes(abs)
	if n > 0 {
		return "+" + text
	}
	return "-" + text
}

func formatBytesNullable(v any) string {
	n, ok := asInt64(v)
	if !ok {
		return "—"
	}
	return formatBytes(n)
}

func formatSignedBytesNullable(v any) string {
	n, ok := asInt64(v)
	if !ok {
		return "—"
	}
	return formatSignedBytes(n)
}

func asInt64(v any) (int64, bool) {
	if v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) {
			return 0, false
		}
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func formatDurationCell(sec float64) string {
	if math.IsNaN(sec) || sec < 0 {
		return "—"
	}
	if sec < 60 {
		return fmt.Sprintf("%.0fs", sec)
	}
	if sec < 3600 {
		m := int(sec) / 60
		s := int(math.Round(sec)) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

func anyInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}

func anyFloat64(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		n, _ := t.Float64()
		return n
	default:
		return 0
	}
}
