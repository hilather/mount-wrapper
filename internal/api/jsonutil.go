package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, data any) {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		http.Error(w, `{"error":"encode failed","code":"ERROR"}`, http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// unwrapControl maps a control-plane response to HTTP status + body.
// Success returns the data payload; failure returns {error, code}.
func unwrapControl(resp map[string]any) (status int, body any) {
	if resp == nil {
		return http.StatusServiceUnavailable, map[string]any{
			"error": "service not available",
			"code":  "UNAVAILABLE",
		}
	}
	ok, _ := resp["ok"].(bool)
	if ok {
		if data, exists := resp["data"]; exists {
			return http.StatusOK, data
		}
		return http.StatusOK, map[string]any{}
	}
	code, _ := resp["code"].(string)
	if code == "" {
		code = "ERROR"
	}
	msg, _ := resp["error"].(string)
	if msg == "" {
		msg = "request failed"
	}
	status = http.StatusBadRequest
	switch code {
	case "UNAVAILABLE":
		status = http.StatusServiceUnavailable
	case "PERMISSION_DENIED":
		status = http.StatusForbidden
	case "NOT_FOUND":
		status = http.StatusNotFound
	case "CONFIG_ERROR", "IO_ERROR", "MOUNT_FAILED", "ERROR", "BAD_REQUEST":
		// keep 400 (or 500 for pure ERROR if preferred — parity uses 400 for ControlError)
	}
	return status, map[string]any{"error": msg, "code": code}
}

func readJSONObject(w http.ResponseWriter, r *http.Request, maxBytes int64) (map[string]any, int, string) {
	if maxBytes <= 0 {
		maxBytes = 2_000_000
	}
	// Pass w so MaxBytesReader can emit 413 without panicking on limit exceed.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		// Empty body → empty object.
		if err.Error() == "EOF" {
			return map[string]any{}, 0, ""
		}
		// Request too large surfaces as MaxBytesError.
		if _, ok := err.(*http.MaxBytesError); ok {
			return nil, http.StatusRequestEntityTooLarge, "request body too large"
		}
		return nil, http.StatusBadRequest, "invalid JSON: " + err.Error()
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, 0, ""
}

func asBool(v any, def bool) bool {
	if v == nil {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch t {
		case "1", "true", "True", "TRUE", "yes", "on":
			return true
		case "0", "false", "False", "FALSE", "no", "off", "":
			return false
		}
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return i != 0
		}
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return def
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
