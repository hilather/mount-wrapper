package convert

import (
	"encoding/json"
	"os"
	"time"
)

// MetadataSuffix is the convert metadata sidecar suffix.
// Parity with tarmount-wsl sevenzip_convert_metadata.METADATA_SUFFIX.
const MetadataSuffix = ".tarmount-convert.json"

// MetadataPath returns the convert metadata sidecar path for archivePath
// (archivePath + MetadataSuffix).
func MetadataPath(archivePath string) string {
	if archivePath == "" {
		return ""
	}
	return archivePath + MetadataSuffix
}

// ConvertMetadata is the convert sidecar JSON payload.
// Parity with tarmount-wsl sevenzip_convert_metadata.ConvertMetadata.
type ConvertMetadata struct {
	OriginalSizeBytes      int64    `json:"original_size_bytes"`
	ConvertedSizeBytes     int64    `json:"converted_size_bytes"`
	ConvertedAt            string   `json:"converted_at"`
	Method                 string   `json:"method"`
	ConvertDurationSeconds *float64 `json:"convert_duration_seconds,omitempty"`
	// SizeDeltaBytes is written on disk (converted − original) for Python parity.
	SizeDeltaBytes int64 `json:"size_delta_bytes"`
}

// SizeDelta returns converted_size_bytes − original_size_bytes.
func (m ConvertMetadata) SizeDelta() int64 {
	return m.ConvertedSizeBytes - m.OriginalSizeBytes
}

// BuildConvertMetadata constructs ConvertMetadata with UTC timestamp.
// method defaults to "flatten" when empty (Python default).
func BuildConvertMetadata(
	originalSizeBytes, convertedSizeBytes int64,
	method string,
	convertDurationSeconds *float64,
) ConvertMetadata {
	if method == "" {
		method = "flatten"
	}
	m := ConvertMetadata{
		OriginalSizeBytes:      originalSizeBytes,
		ConvertedSizeBytes:     convertedSizeBytes,
		ConvertedAt:            time.Now().UTC().Format(time.RFC3339),
		Method:                 method,
		ConvertDurationSeconds: convertDurationSeconds,
	}
	m.SizeDeltaBytes = m.SizeDelta()
	return m
}

// WriteConvertMetadata writes the sidecar JSON next to archivePath.
// Returns the sidecar path. Uses indent=2, sort keys via encoding/json
// struct field order (matches Python sort_keys payload content).
func WriteConvertMetadata(archivePath string, meta ConvertMetadata) (string, error) {
	if archivePath == "" {
		return "", &Error{Op: "write_metadata", Msg: "empty archive path"}
	}
	if meta.Method == "" {
		meta.Method = "flatten"
	}
	meta.SizeDeltaBytes = meta.SizeDelta()
	path := MetadataPath(archivePath)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", &Error{Op: "write_metadata", Msg: err.Error()}
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", &Error{Op: "write_metadata", Path: path, Msg: err.Error()}
	}
	return path, nil
}

// ReadConvertMetadata loads the sidecar for archivePath, or nil when missing
// / unreadable / invalid (parity with Python returning None).
func ReadConvertMetadata(archivePath string) *ConvertMetadata {
	if archivePath == "" {
		return nil
	}
	path := MetadataPath(archivePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	orig, ok1 := asInt64(raw["original_size_bytes"])
	conv, ok2 := asInt64(raw["converted_size_bytes"])
	at, ok3 := raw["converted_at"].(string)
	if !ok1 || !ok2 || !ok3 || at == "" {
		return nil
	}
	method := "flatten"
	if m, ok := raw["method"].(string); ok && m != "" {
		method = m
	}
	meta := &ConvertMetadata{
		OriginalSizeBytes:  orig,
		ConvertedSizeBytes: conv,
		ConvertedAt:        at,
		Method:             method,
		SizeDeltaBytes:     conv - orig,
	}
	if v, ok := raw["convert_duration_seconds"]; ok && v != nil {
		if d, ok := asFloat64(v); ok {
			meta.ConvertDurationSeconds = &d
		}
	}
	if sd, ok := asInt64(raw["size_delta_bytes"]); ok {
		meta.SizeDeltaBytes = sd
	}
	return meta
}

// HasConvertMetadata reports whether a readable convert sidecar exists.
func HasConvertMetadata(archivePath string) bool {
	return ReadConvertMetadata(archivePath) != nil
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func asFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
