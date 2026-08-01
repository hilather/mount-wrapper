package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// formatStatusHuman renders a status payload for non-JSON CLI output.
// Accepts the control-plane data object (map) from the status op.
func formatStatusHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "mount-wrapper: (empty status)\n"
	}
	var b strings.Builder
	ver := anyString(m["version"], "?")
	pid := anyString(m["pid"], "?")
	fmt.Fprintf(&b, "mount-wrapper %s  pid=%s\n", ver, pid)
	if cp := anyString(m["config_path"], ""); cp != "" {
		fmt.Fprintf(&b, "  config: %s\n", cp)
	}

	// Prefer nested counts map (Go StatusPayload); fall back to top-level keys.
	counts := map[string]int{}
	if raw, ok := m["counts"].(map[string]any); ok {
		for k, v := range raw {
			counts[k] = anyInt(v)
		}
	} else {
		for _, key := range statusCountKeys {
			if n := anyInt(m[key]); n > 0 {
				counts[key] = n
			}
		}
	}
	if len(counts) == 0 {
		b.WriteString("  (no archives tracked)\n")
	} else {
		keys := make([]string, 0, len(counts))
		for k, n := range counts {
			if n > 0 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		ordered := make([]string, 0, len(keys))
		seen := map[string]bool{}
		for _, k := range statusCountKeys {
			if n, ok := counts[k]; ok && n > 0 {
				ordered = append(ordered, fmt.Sprintf("%s=%d", k, n))
				seen[k] = true
			}
		}
		for _, k := range keys {
			if !seen[k] {
				ordered = append(ordered, fmt.Sprintf("%s=%d", k, counts[k]))
			}
		}
		fmt.Fprintf(&b, "  %s\n", strings.Join(ordered, "  "))
	}

	if ls := anyString(m["last_scan_at"], ""); ls != "" {
		fmt.Fprintf(&b, "  last_scan_at: %s\n", ls)
	}
	if low, ok := m["low_disk"].(bool); ok && low {
		b.WriteString("  LOW DISK\n")
	}

	archives := anySlice(m["archives"])
	if len(archives) > 0 {
		b.WriteString("  archives:\n")
		for _, raw := range archives {
			a, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := anyString(a["archive_id"], "?")
			st := anyString(a["status"], "?")
			path := anyString(a["archive_path"], "")
			if path == "" {
				path = anyString(a["archive_basename"], id)
			}
			line := fmt.Sprintf("    [%s] %s  %s", st, shortID(id), path)
			if mp := anyString(a["mount_path"], ""); mp != "" {
				line += "  → " + mp
			}
			if errMsg := anyString(a["last_error"], ""); errMsg != "" {
				line += "  err=" + errMsg
			} else if sum := anyString(a["nested_skips_summary"], ""); sum != "" {
				// Mounted success may only expose nested_skips_* when last_error was cleared.
				line += "  nested=" + sum
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

var statusCountKeys = []string{
	"mounted",
	"converting",
	"indexing",
	"mounting",
	"discovered",
	"hooks_running",
	"index_failed",
	"mount_failed",
	"convert_failed",
	"absent",
	"purged",
}

func anyString(v any, def string) string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return def
		}
		return t
	case fmt.Stringer:
		s := t.String()
		if s == "" {
			return def
		}
		return s
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case json.Number:
		return t.String()
	case nil:
		return def
	default:
		s := fmt.Sprintf("%v", t)
		if s == "" || s == "<nil>" {
			return def
		}
		return s
	}
}

func anyInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	default:
		return 0
	}
}

func anySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	default:
		return nil
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
