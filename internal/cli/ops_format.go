package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// printControlData writes either indented JSON (--json) or a human formatter.
func printControlData(stdout, stderr io.Writer, data any, jsonOut bool, formatHuman func(any) string) int {
	if jsonOut {
		if err := printJSON(stdout, data); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	fmt.Fprint(stdout, formatHuman(data))
	return ExitOK
}

// formatRescanHuman renders a control "rescan" summary for non-JSON CLI.
//
// Expected shape (service.doScan):
//
//	seen, inserted, reappeared, content_changed, absent, stable, duration_ms,
//	assume_stable, optional errors []string, optional error string
func formatRescanHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "rescan: ok\n"
	}
	if errMsg := anyString(m["error"], ""); errMsg != "" {
		// Scan failed but control returned ok with error field.
		return fmt.Sprintf("rescan failed: %s\n", errMsg)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "rescan: seen=%d inserted=%d reappeared=%d content_changed=%d absent=%d stable=%d",
		anyInt(m["seen"]),
		anyInt(m["inserted"]),
		anyInt(m["reappeared"]),
		anyInt(m["content_changed"]),
		anyInt(m["absent"]),
		anyInt(m["stable"]),
	)
	if v, ok := m["duration_ms"]; ok && v != nil {
		// duration_ms may be float from JSON or int from in-process.
		ms := anyFloat64(v)
		if ms == float64(int64(ms)) {
			fmt.Fprintf(&b, " duration_ms=%d", int64(ms))
		} else {
			fmt.Fprintf(&b, " duration_ms=%.1f", ms)
		}
	}
	if assume, ok := m["assume_stable"].(bool); ok && assume {
		b.WriteString(" assume_stable=true")
	}
	b.WriteString("\n")
	if errs := anyStringSlice(m["errors"]); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(&b, "  error: %s\n", e)
		}
	}
	return b.String()
}

// formatRetryHuman renders a control "retry" archive dict for non-JSON CLI.
func formatRetryHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "retry: ok\n"
	}
	id := anyString(m["archive_id"], "?")
	st := anyString(m["status"], "?")
	attempts := anyInt(m["mount_attempts"])
	return fmt.Sprintf("retry archive_id=%s status=%s mount_attempts=%d\n", id, st, attempts)
}

// formatMountHuman renders a control "mount" response for non-JSON CLI.
//
// Shapes:
//
//	{"archive_id","status","queued":true}
//	{"archive_id","pid","mount_path","status"}
func formatMountHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "mount: ok\n"
	}
	id := anyString(m["archive_id"], "?")
	st := anyString(m["status"], "?")
	if queued, ok := m["queued"].(bool); ok && queued {
		return fmt.Sprintf("mount queued archive_id=%s status=%s\n", id, st)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mount started archive_id=%s status=%s", id, st)
	if _, has := m["pid"]; has && m["pid"] != nil {
		fmt.Fprintf(&b, " pid=%s", anyString(m["pid"], ""))
	}
	if mp := anyString(m["mount_path"], ""); mp != "" {
		fmt.Fprintf(&b, " mount_path=%s", mp)
	}
	b.WriteString("\n")
	return b.String()
}

// formatUnmountHuman renders a control "unmount" response for non-JSON CLI.
//
// Shapes:
//
//	archive dict (single target)
//	{"unmounted":[…]}  (--all; each item archive dict or {archive_id,error})
func formatUnmountHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "unmount: ok\n"
	}
	if raw, has := m["unmounted"]; has {
		list := anySlice(raw)
		var b strings.Builder
		fmt.Fprintf(&b, "unmount --all: %d archive(s)\n", len(list))
		if len(list) == 0 {
			b.WriteString("  (none unmounted)\n")
			return b.String()
		}
		for _, item := range list {
			row, ok := item.(map[string]any)
			if !ok || row == nil {
				continue
			}
			id := anyString(row["archive_id"], "?")
			if errMsg := anyString(row["error"], ""); errMsg != "" {
				fmt.Fprintf(&b, "  error archive_id=%s: %s\n", id, errMsg)
				continue
			}
			st := anyString(row["status"], "?")
			fmt.Fprintf(&b, "  unmounted archive_id=%s status=%s\n", id, st)
		}
		return b.String()
	}
	// Single-target archive dict.
	id := anyString(m["archive_id"], "?")
	st := anyString(m["status"], "?")
	return fmt.Sprintf("unmounted archive_id=%s status=%s\n", id, st)
}

// formatPurgeHuman renders a control "purge" response for non-JSON CLI.
func formatPurgeHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "purge: ok\n"
	}
	id := anyString(m["archive_id"], "?")
	idx := m["index_deleted"]
	overlay := anyString(m["overlay_action"], "")
	mountCleaned := m["mount_cleaned"]
	return fmt.Sprintf("purged archive_id=%s index_deleted=%s overlay_action=%s mount_cleaned=%s\n",
		id, formatBoolish(idx), overlay, formatBoolish(mountCleaned))
}

// formatHooksListHuman renders control "hooks_list" for non-JSON CLI.
//
//	{"hooks":[{"name","path"},…]}
func formatHooksListHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "hooks: (empty)\n"
	}
	list := anySlice(m["hooks"])
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString("hooks: (none)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "hooks (%d):\n", len(list))
	// Stable order by name then path.
	rows := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if row, ok := item.(map[string]any); ok && row != nil {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ni := anyString(rows[i]["name"], "")
		nj := anyString(rows[j]["name"], "")
		if ni != nj {
			return ni < nj
		}
		return anyString(rows[i]["path"], "") < anyString(rows[j]["path"], "")
	})
	for _, row := range rows {
		name := anyString(row["name"], "?")
		path := anyString(row["path"], "")
		if path != "" {
			fmt.Fprintf(&b, "  %s  %s\n", name, path)
		} else {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	return b.String()
}

// formatHooksStatusHuman renders control "hooks_status" for non-JSON CLI.
//
//	{"archive_id","hooks_status","hooks":[{hook_name,status,attempts,…}]}
func formatHooksStatusHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "hooks status: (empty)\n"
	}
	id := anyString(m["archive_id"], "?")
	hs := anyString(m["hooks_status"], "?")
	var b strings.Builder
	fmt.Fprintf(&b, "hooks status archive_id=%s hooks_status=%s\n", id, hs)
	list := anySlice(m["hooks"])
	if len(list) == 0 {
		b.WriteString("  (no hook rows)\n")
		return b.String()
	}
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok || row == nil {
			continue
		}
		name := anyString(row["hook_name"], "?")
		st := anyString(row["status"], "?")
		attempts := anyInt(row["attempts"])
		line := fmt.Sprintf("  [%s] %s attempts=%d", st, name, attempts)
		if _, has := row["last_exit_code"]; has && row["last_exit_code"] != nil {
			line += fmt.Sprintf(" exit=%s", anyString(row["last_exit_code"], ""))
		}
		if errMsg := anyString(row["last_error"], ""); errMsg != "" {
			line += " err=" + errMsg
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// formatBoolish prints bool-ish JSON values without Go's default %v for other types.
func formatBoolish(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return "?"
	default:
		return fmt.Sprintf("%v", t)
	}
}

func anyStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
