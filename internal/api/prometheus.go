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
//
// Size/savings gauges (default on): requests control status with include_sizes so
// metrics_summary can drive aggregate totals. If summary is missing, falls back to
// the metrics op. Omit with ServerOptions.PrometheusOmitSizeGauges for a fast
// count-only scrape. Never emits high-cardinality per-archive series.
func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	includeSizes := !s.opts.PrometheusOmitSizeGauges
	req := map[string]any{"op": "status"}
	if includeSizes {
		req["include_sizes"] = true
	}
	resp := s.backend.HandleRequest(req)
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

	var sizeSummary map[string]any
	if includeSizes {
		sizeSummary = asMap(st["metrics_summary"])
		if sizeSummary == nil {
			// Fallback when status omit metrics_summary (e.g. Metrics unset on
			// status path) but the metrics control op is still available.
			mresp := s.backend.HandleRequest(map[string]any{"op": "metrics"})
			if mcode, mbody := unwrapControl(mresp); mcode == http.StatusOK {
				if m := asMap(mbody); m != nil {
					sizeSummary = asMap(m["summary"])
				}
			}
		}
	}

	version := s.backend.Version()
	if version == "" {
		version = s.version
	}
	if v := asString(st["version"]); v != "" {
		version = v
	}

	text := renderPrometheus(st, version, sizeSummary)
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

// renderPrometheus builds Prometheus text exposition from a status map and
// optional size/savings summary (metrics_summary or metrics op summary).
// sizeSummary may be nil to omit size gauges (fast scrape or unavailable).
func renderPrometheus(st map[string]any, version string, sizeSummary map[string]any) string {
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

	// --- size / savings summary gauges (aggregates only; no per-archive labels) ---
	if sizeSummary != nil {
		writePromSizeGauges(&b, sizeSummary)
	}

	// --- mount_wrapper_info ---
	if version == "" {
		version = "unknown"
	}
	b.WriteString("# HELP mount_wrapper_info Mount-wrapper process info (always 1).\n")
	b.WriteString("# TYPE mount_wrapper_info gauge\n")
	fmt.Fprintf(&b, "mount_wrapper_info{version=%s} 1\n", promLabelValue(version))

	return b.String()
}

// promSizeGauge is one aggregate size/savings metric mapped from metrics_summary.
type promSizeGauge struct {
	name string
	help string
	// key is the metrics_summary / Summary JSON field name.
	key string
	// optional: when true, omit the series if the key is absent (nullable fields).
	optional bool
}

// promSizeGauges is the stable emission order for size/savings scrapes.
var promSizeGauges = []promSizeGauge{
	{
		name: "mount_wrapper_archive_size_bytes",
		help: "Total size in bytes of tracked archive files (sum of known archive_size_bytes).",
		key:  "total_archive_size_bytes",
	},
	{
		name: "mount_wrapper_index_size_bytes",
		help: "Total size in bytes of ratarmount index files (sum of known index_size_bytes).",
		key:  "total_index_size_bytes",
	},
	{
		name: "mount_wrapper_extracted_size_bytes",
		help: "Total logical extracted size in bytes (sum of known extracted_size_bytes).",
		key:  "total_extracted_size_bytes",
	},
	{
		name: "mount_wrapper_space_saved_bytes",
		help: "Total space saved vs full extract: sum of max(0, extracted − index) over archives with both sizes.",
		key:  "total_space_saved_bytes",
	},
	{
		name: "mount_wrapper_archives_with_extracted_size",
		help: "Number of tracked archives with a known extracted (logical) size.",
		key:  "archives_with_extracted_size",
	},
	{
		name: "mount_wrapper_archives_with_convert_metadata",
		help: "Number of tracked archives with convert source-size metadata.",
		key:  "archives_with_convert_metadata",
	},
	{
		name:     "mount_wrapper_convert_source_size_bytes",
		help:     "Total convert source size in bytes when convert metadata is present.",
		key:      "total_convert_source_size_bytes",
		optional: true,
	},
	{
		name:     "mount_wrapper_convert_size_delta_bytes",
		help:     "Total convert size delta (archive − source) in bytes when convert metadata is present.",
		key:      "total_convert_size_delta_bytes",
		optional: true,
	},
	{
		name:     "mount_wrapper_max_convert_duration_seconds",
		help:     "Maximum convert duration in seconds among archives with convert duration metadata.",
		key:      "max_convert_duration_seconds",
		optional: true,
	},
}

func writePromSizeGauges(b *strings.Builder, sum map[string]any) {
	for _, g := range promSizeGauges {
		v, ok := sum[g.key]
		if !ok || v == nil {
			if g.optional {
				continue
			}
			// Required totals: emit 0 when key missing so scrapes stay stable.
			b.WriteString("# HELP ")
			b.WriteString(g.name)
			b.WriteByte(' ')
			b.WriteString(g.help)
			b.WriteByte('\n')
			b.WriteString("# TYPE ")
			b.WriteString(g.name)
			b.WriteString(" gauge\n")
			b.WriteString(g.name)
			b.WriteString(" 0\n")
			continue
		}
		b.WriteString("# HELP ")
		b.WriteString(g.name)
		b.WriteByte(' ')
		b.WriteString(g.help)
		b.WriteByte('\n')
		b.WriteString("# TYPE ")
		b.WriteString(g.name)
		b.WriteString(" gauge\n")
		b.WriteString(g.name)
		b.WriteByte(' ')
		b.WriteString(formatPromFloat(anyToFloat(v)))
		b.WriteByte('\n')
	}
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
