package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/hilather/mount-wrapper/internal/doctor"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	cfg := s.backend.Config()
	bind := s.bind
	controlSocket := ""
	if cfg != nil {
		controlSocket = cfg.ControlSocket
		if bind == "" {
			bind = cfg.WebHost + ":" + strconv.Itoa(cfg.WebPort)
		}
	}

	// In-process: serve is reachable when backend can answer status.
	resp := s.backend.HandleRequest(map[string]any{"op": "status"})
	statusCode, payload := unwrapControl(resp)
	reachable := statusCode == http.StatusOK
	var servicePID any
	var serviceVersion any
	var serviceError any
	if reachable {
		if m := asMap(payload); m != nil {
			servicePID = m["pid"]
			serviceVersion = m["version"]
		}
	} else if m := asMap(payload); m != nil {
		serviceError = m["error"]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"web_version":         s.version,
		"service_reachable":   reachable,
		"control_socket":      controlSocket,
		"bind":                bind,
		"service_status_code": statusCode,
		"service_error":       serviceError,
		"service_pid":         servicePID,
		"service_version":     serviceVersion,
		"pid":                 os.Getpid(),
		"version":             s.backend.Version(),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	includeSizes := strings.HasSuffix(r.URL.Path, "/sizes") ||
		r.URL.Query().Get("include_sizes") == "1" ||
		r.URL.Query().Get("include_sizes") == "true"
	resp := s.backend.HandleRequest(map[string]any{
		"op":            "status",
		"include_sizes": includeSizes,
	})
	status, body := unwrapControl(resp)
	writeJSON(w, status, body)
}

func (s *Server) handleArchives(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	// Merge status + metrics (parity build_archives_payload).
	stCode, statusBody := unwrapControl(s.backend.HandleRequest(map[string]any{"op": "status"}))
	if stCode != http.StatusOK {
		writeJSON(w, stCode, statusBody)
		return
	}
	status := asMap(statusBody)
	if status == nil {
		status = map[string]any{}
	}

	mCode, metricsBody := unwrapControl(s.backend.HandleRequest(map[string]any{"op": "metrics"}))
	metricsList := extractMetricsList(metricsBody)
	var summary any
	if mCode == http.StatusOK {
		if m := asMap(metricsBody); m != nil {
			summary = m["summary"]
		}
	}

	byID := map[string]any{}
	for _, item := range metricsList {
		if id := asString(item["archive_id"]); id != "" {
			byID[id] = item
		}
	}

	archivesRaw := asSliceOfMaps(status["archives"])
	merged := make([]any, 0, len(archivesRaw))
	for _, row := range archivesRaw {
		out := make(map[string]any, len(row)+1)
		for k, v := range row {
			out[k] = v
		}
		aid := asString(row["archive_id"])
		if m, ok := byID[aid]; ok {
			out["metrics"] = m
		} else if _, has := out["metrics"]; !has {
			out["metrics"] = nil
		}
		merged = append(merged, out)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"archives":          merged,
		"summary":           summary,
		"counts":            status["counts"],
		"mounted":           status["mounted"],
		"indexing":          status["indexing"],
		"mounting":          status["mounting"],
		"discovered":        status["discovered"],
		"hooks_running":     status["hooks_running"],
		"index_failed":      status["index_failed"],
		"mount_failed":      status["mount_failed"],
		"absent":            status["absent"],
		"version":           status["version"],
		"pid":               status["pid"],
		"low_disk":          status["low_disk"],
		"last_scan_at":      status["last_scan_at"],
		"indexing_archives": status["indexing_archives"],
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	q := r.URL.Query()
	req := map[string]any{
		"op":           "metrics",
		"no_cache":     q.Get("no_cache") == "1" || q.Get("no_cache") == "true",
		"prefer_mount": q.Get("prefer_mount") == "1" || q.Get("prefer_mount") == "true",
	}
	if id := q.Get("archive_id"); id != "" {
		req["archive_id"] = id
	}
	status, body := unwrapControl(s.backend.HandleRequest(req))
	writeJSON(w, status, body)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status, body := unwrapControl(s.backend.HandleRequest(map[string]any{"op": "config_get"}))
		writeJSON(w, status, body)
	case http.MethodPost:
		body, code, errMsg := readJSONObject(w, r, 2_000_000)
		if code != 0 {
			writeJSON(w, code, map[string]any{"error": errMsg, "code": "BAD_REQUEST"})
			return
		}
		fields := map[string]any{
			"op":    "config_set",
			"apply": asBool(body["apply"], true),
		}
		if c, ok := body["config"].(map[string]any); ok {
			fields["config"] = c
		}
		if p, ok := body["patch"].(map[string]any); ok {
			fields["patch"] = p
		}
		if _, hasC := fields["config"]; !hasC {
			if _, hasP := fields["patch"]; !hasP {
				// Treat entire body as full config except apply flag.
				payload := make(map[string]any, len(body))
				for k, v := range body {
					if k == "apply" {
						continue
					}
					payload[k] = v
				}
				fields["config"] = payload
			}
		}
		status, out := unwrapControl(s.backend.HandleRequest(fields))
		writeJSON(w, status, out)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
	}
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	if s.limitDestructive != nil && !s.limitDestructive.allow(clientKey(r)+"|rescan") {
		writeRateLimited(w)
		return
	}
	body, code, errMsg := readJSONObject(w, r, 2_000_000)
	if code != 0 {
		writeJSON(w, code, map[string]any{"error": errMsg, "code": "BAD_REQUEST"})
		return
	}
	status, out := unwrapControl(s.backend.HandleRequest(map[string]any{
		"op":            "rescan",
		"assume_stable": asBool(body["assume_stable"], false),
	}))
	writeJSON(w, status, out)
}

func (s *Server) handleUnmount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	body, code, errMsg := readJSONObject(w, r, 2_000_000)
	if code != 0 {
		writeJSON(w, code, map[string]any{"error": errMsg, "code": "BAD_REQUEST"})
		return
	}
	fields := map[string]any{"op": "unmount"}
	if asBool(body["all"], false) {
		// unmount-all is destructive; single-target unmount is not rate-limited.
		if s.limitDestructive != nil && !s.limitDestructive.allow(clientKey(r)+"|unmount-all") {
			writeRateLimited(w)
			return
		}
		fields["all"] = true
	} else if t := asString(body["target"]); t != "" {
		fields["target"] = t
	} else if id := asString(body["archive_id"]); id != "" {
		fields["target"] = id
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "target, archive_id, or all required",
			"code":  "BAD_REQUEST",
		})
		return
	}
	status, out := unwrapControl(s.backend.HandleRequest(fields))
	writeJSON(w, status, out)
}

func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	body, code, errMsg := readJSONObject(w, r, 2_000_000)
	if code != 0 {
		writeJSON(w, code, map[string]any{"error": errMsg, "code": "BAD_REQUEST"})
		return
	}
	id := asString(body["archive_id"])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "archive_id required", "code": "BAD_REQUEST"})
		return
	}
	status, out := unwrapControl(s.backend.HandleRequest(map[string]any{
		"op":         "retry",
		"archive_id": id,
	}))
	writeJSON(w, status, out)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	body, code, errMsg := readJSONObject(w, r, 2_000_000)
	if code != 0 {
		writeJSON(w, code, map[string]any{"error": errMsg, "code": "BAD_REQUEST"})
		return
	}
	id := asString(body["archive_id"])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "archive_id required", "code": "BAD_REQUEST"})
		return
	}
	if !asBool(body["yes"], false) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "purge requires yes: true",
			"code":  "BAD_REQUEST",
		})
		return
	}
	// Rate-limit only after validation so clients still see 400 for bad bodies.
	if s.limitDestructive != nil && !s.limitDestructive.allow(clientKey(r)+"|purge") {
		writeRateLimited(w)
		return
	}
	status, out := unwrapControl(s.backend.HandleRequest(map[string]any{
		"op":         "purge",
		"archive_id": id,
		"yes":        true,
	}))
	writeJSON(w, status, out)
}

func (s *Server) handleHooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	// Per-archive hooks status (control op hooks_status). Optional list of
	// discovered hooks.d scripts when archive_id is omitted (hooks_list).
	id := strings.TrimSpace(r.URL.Query().Get("archive_id"))
	if id == "" {
		status, body := unwrapControl(s.backend.HandleRequest(map[string]any{"op": "hooks_list"}))
		writeJSON(w, status, body)
		return
	}
	status, body := unwrapControl(s.backend.HandleRequest(map[string]any{
		"op":         "hooks_status",
		"archive_id": id,
	}))
	writeJSON(w, status, body)
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	// In-process; does not require control socket.
	report := doctor.Run(doctor.Options{
		Config:     s.backend.Config(),
		FixSystemd: false,
	})
	writeJSON(w, http.StatusOK, report.ToMap())
}

func (s *Server) handleWSLInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}
	mountRoot := ""
	if cfg := s.backend.Config(); cfg != nil {
		mountRoot = cfg.MountRoot
	}
	writeJSON(w, http.StatusOK, buildWSLInfo(mountRoot, os.Getenv("WSL_DISTRO_NAME")))
}

// buildWSLInfo returns Windows UNC access hints (parity web.py build_wsl_info).
func buildWSLInfo(mountRoot, distro string) map[string]any {
	var unc any
	hint := "WSL distro name not detected; run `wsl.exe -l -v` on Windows."
	if distro != "" && mountRoot != "" {
		linuxPath := mountRoot
		if !strings.HasPrefix(linuxPath, "/") {
			linuxPath = "/" + linuxPath
		}
		uncPath := strings.ReplaceAll(linuxPath, "/", `\`)
		unc = `\\wsl.localhost\` + distro + uncPath
		hint = "From Windows Explorer, open the UNC path (exact distro name from wsl -l -v)."
	} else if distro != "" {
		hint = "From Windows Explorer, open \\\\wsl.localhost\\" + distro + "\\…"
	}
	var distroVal any
	if distro != "" {
		distroVal = distro
	}
	return map[string]any{
		"distro_name": distroVal,
		"mount_root":  mountRoot,
		"unc_mounts":  unc,
		"hint":        hint,
	}
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	// Typed payloads (*status.Payload, etc.) → map via JSON.
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func asSliceOfMaps(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func extractMetricsList(body any) []map[string]any {
	m := asMap(body)
	if m == nil {
		return nil
	}
	return asSliceOfMaps(m["metrics"])
}
