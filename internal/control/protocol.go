package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ProtocolVersion is the only supported control-plane protocol version.
const ProtocolVersion = 1

// Error is a control-plane protocol or transport error.
type Error struct {
	Message string
	Code    string
}

func (e *Error) Error() string {
	if e == nil {
		return "control error"
	}
	return e.Message
}

// NewError builds a control Error with an optional machine-readable code.
func NewError(message, code string) *Error {
	if code == "" {
		code = "ERROR"
	}
	return &Error{Message: message, Code: code}
}

// OKResponse wraps data as {"ok":true,"data":...}.
// When data is nil, data is an empty object (parity with Python ok_response).
func OKResponse(data any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	return map[string]any{"ok": true, "data": data}
}

// ErrResponse wraps an error as {"ok":false,"error":"...","code":"..."}.
func ErrResponse(message, code string) map[string]any {
	if code == "" {
		code = "ERROR"
	}
	return map[string]any{"ok": false, "error": message, "code": code}
}

// ParseRequest parses one JSON request line; validates version and op.
// Missing "v" is treated as ProtocolVersion. Unknown version → UNSUPPORTED_VERSION.
func ParseRequest(line string) (map[string]any, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, NewError("empty request", "BAD_REQUEST")
	}
	var raw any
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, NewError(fmt.Sprintf("invalid JSON: %v", err), "BAD_REQUEST")
	}
	req, ok := raw.(map[string]any)
	if !ok {
		return nil, NewError("request must be a JSON object", "BAD_REQUEST")
	}
	version := ProtocolVersion
	if v, present := req["v"]; present {
		parsed, ok := asInt(v)
		if !ok {
			return nil, NewError(fmt.Sprintf("unsupported protocol version %v", v), "UNSUPPORTED_VERSION")
		}
		version = parsed
	}
	if version != ProtocolVersion {
		return nil, NewError(fmt.Sprintf("unsupported protocol version %v", version), "UNSUPPORTED_VERSION")
	}
	op, _ := req["op"].(string)
	if strings.TrimSpace(op) == "" {
		return nil, NewError("missing op", "BAD_REQUEST")
	}
	req["v"] = version
	req["op"] = op
	return req, nil
}

// EncodeResponse serializes a response map as one JSON line (trailing newline).
func EncodeResponse(resp map[string]any) ([]byte, error) {
	if resp == nil {
		resp = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Compact encoding (parity with Python separators=(",", ":")).
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		return nil, err
	}
	// json.Encoder already appends '\n'.
	return buf.Bytes(), nil
}

// EncodeRequest builds a request payload and encodes it as one JSON line.
func EncodeRequest(op string, fields map[string]any) ([]byte, error) {
	payload := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		payload[k] = v
	}
	payload["v"] = ProtocolVersion
	payload["op"] = op
	return EncodeResponse(payload)
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
		return 0, false
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}
