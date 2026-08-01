package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/status"
)

// Prometheus exposition Content-Type (text format 0.0.4).
const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// knownArchiveStatuses is the stable label set for mount_wrapper_archives.
// Order matches status package counts for readable scrapes.
var knownArchiveStatuses = []string{
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

// handlePrometheus serves GET /metrics in Prometheus text format.
//
// Auth policy:
//   - Loopback bind (127.0.0.1 / ::1 / localhost): always open — scrapers need no token.
//   - Non-loopback bind: same as /api/* (empty web_token = open; else Bearer / ?token=).
func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := s.backend.HandleRequest(map[string]any{"op": "status"})
	code, body := unwrapControl(resp)
	if code != http.StatusOK {
		// Prefer plain text for scrapers when status is unavailable.
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	st := asMap(body)
	if st == nil {
		st = map[string]any{}
	}

	version := s.backend.Version()
	if version == "" {
		version = s.version
	}
	if v := asString(st["version"]); v != "" {
		version = v
	}

	text := renderPrometheus(st, version)
	w.Header().Set("Content-Type", prometheusContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(text))
}

// withMetricsAuth applies the /metrics auth policy (see handlePrometheus).
func (s *Server) withMetricsAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isLoopbackHost(bindHost(s.bind)) {
			next(w, r)
			return
		}
		if !checkToken(r, s.token) {
			// Plain text 401 so Prometheus scrapers get a clear failure (not JSON).
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// bindHost extracts the host portion of host:port (or returns the whole string).
func bindHost(bind string) string {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return strings.TrimSpace(bind)
	}
	return host
}

// isLoopbackHost reports whether host is a loopback address or name.
func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	switch h {
	case "127.0.0.1", "localhost", "::1", "":
		return true
	}
	// Bracketed IPv6 from some bind forms.
	h = strings.Trim(h, "[]")
	if h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// renderPrometheus builds Prometheus text exposition from a status map.
func renderPrometheus(st map[string]any, version string) string {
	var b strings.Builder

	// --- mount_wrapper_archives ---
	counts := map[string]float64{}
	for _, k := range knownArchiveStatuses {
		counts[k] = 0
	}
	if raw := asMap(st["counts"]); raw != nil {
		for k, v := range raw {
			counts[k] = anyToFloat(v)
		}
	} else {
		// Fall back to top-level convenience counts when counts map is absent.
		for _, k := range knownArchiveStatuses {
			if v, ok := st[k]; ok {
				counts[k] = anyToFloat(v)
			}
		}
	}
	// Stable emission order: known first, then any extra statuses sorted.
	b.WriteString("# HELP mount_wrapper_archives Number of tracked archives by lifecycle status.\n")
	b.WriteString("# TYPE mount_wrapper_archives gauge\n")
	seen := map[string]struct{}{}
	for _, k := range knownArchiveStatuses {
		seen[k] = struct{}{}
		fmt.Fprintf(&b, "mount_wrapper_archives{status=%q} %s\n", k, formatPromFloat(counts[k]))
	}
	var extra []string
	for k := range counts {
		if _, ok := seen[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		fmt.Fprintf(&b, "mount_wrapper_archives{status=%q} %s\n", k, formatPromFloat(counts[k]))
	}

	// --- mount_wrapper_low_disk ---
	low := 0.0
	if asBool(st["low_disk"], false) {
		low = 1
	}
	b.WriteString("# HELP mount_wrapper_low_disk 1 if free disk is below min_free_bytes, else 0.\n")
	b.WriteString("# TYPE mount_wrapper_low_disk gauge\n")
	fmt.Fprintf(&b, "mount_wrapper_low_disk %s\n", formatPromFloat(low))

	// --- mount_wrapper_last_scan_timestamp_seconds ---
	lastScan := 0.0
	if iso := asString(st["last_scan_at"]); iso != "" {
		if ep := status.ParseISOToEpoch(iso); ep != nil {
			lastScan = *ep
		} else if t, err := time.Parse(time.RFC3339, iso); err == nil {
			lastScan = float64(t.Unix())
		}
	}
	b.WriteString("# HELP mount_wrapper_last_scan_timestamp_seconds Unix timestamp of the last discovery scan (0 if never).\n")
	b.WriteString("# TYPE mount_wrapper_last_scan_timestamp_seconds gauge\n")
	fmt.Fprintf(&b, "mount_wrapper_last_scan_timestamp_seconds %s\n", formatPromFloat(lastScan))

	// --- mount_wrapper_info ---
	if version == "" {
		version = "unknown"
	}
	b.WriteString("# HELP mount_wrapper_info Mount-wrapper process info (always 1).\n")
	b.WriteString("# TYPE mount_wrapper_info gauge\n")
	fmt.Fprintf(&b, "mount_wrapper_info{version=%s} 1\n", promLabelValue(version))

	return b.String()
}

// promLabelValue returns a double-quoted, escaped Prometheus label value.
func promLabelValue(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func anyToFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case uint:
		return float64(t)
	case uint64:
		return float64(t)
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return f
		}
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f
		}
	case bool:
		if t {
			return 1
		}
		return 0
	}
	return 0
}

// formatPromFloat formats a float for Prometheus text (no unnecessary decimals).
func formatPromFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
