package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var durationRE = regexp.MustCompile(`(?i)^\s*(?P<value>\d+(?:\.\d+)?)\s*(?P<unit>ms|s|m|h|d)?\s*$`)

// ParseDuration parses a duration into seconds.
//
// Accepts:
//   - numeric types (interpreted as seconds)
//   - strings: 60, 60s, 30m, 24h, 7d, 500ms
//
// Boolean values are rejected. fieldName is used in error messages.
func ParseDuration(value any, fieldName string) (float64, error) {
	if fieldName == "" {
		fieldName = "duration"
	}
	switch v := value.(type) {
	case bool:
		return 0, &ConfigError{Message: fmt.Sprintf("%s: expected duration, got boolean", fieldName)}
	case int:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case int8:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case int16:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case int32:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case int64:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case float32:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return float64(v), nil
	case float64:
		if v < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0, got %v", fieldName, v)}
		}
		return v, nil
	case string:
		match := durationRE.FindStringSubmatch(v)
		if match == nil {
			return 0, &ConfigError{Message: fmt.Sprintf(
				"%s: invalid duration %q (use e.g. 60, 60s, 30m, 24h, 7d)",
				fieldName, v,
			)}
		}
		// groups: full, value, unit
		amount, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: invalid duration %q", fieldName, v)}
		}
		unit := strings.ToLower(match[2])
		if unit == "" {
			unit = "s"
		}
		var mult float64
		switch unit {
		case "ms":
			mult = 0.001
		case "s":
			mult = 1
		case "m":
			mult = 60
		case "h":
			mult = 3600
		case "d":
			mult = 86400
		default:
			return 0, &ConfigError{Message: fmt.Sprintf("%s: invalid duration %q", fieldName, v)}
		}
		seconds := amount * mult
		if seconds < 0 {
			return 0, &ConfigError{Message: fmt.Sprintf("%s: duration must be >= 0", fieldName)}
		}
		return seconds, nil
	default:
		return 0, &ConfigError{Message: fmt.Sprintf(
			"%s: expected duration string or number, got %T", fieldName, value,
		)}
	}
}

// FormatDuration formats seconds as a compact human duration for round-trip display.
func FormatDuration(seconds float64) string {
	if seconds == 0 {
		return "0s"
	}
	if seconds < 1 {
		return fmt.Sprintf("%dms", int(math.Round(seconds*1000)))
	}
	if math.Mod(seconds, 86400) == 0 {
		return fmt.Sprintf("%dd", int(seconds/86400))
	}
	if math.Mod(seconds, 3600) == 0 {
		return fmt.Sprintf("%dh", int(seconds/3600))
	}
	if math.Mod(seconds, 60) == 0 {
		return fmt.Sprintf("%dm", int(seconds/60))
	}
	if seconds == float64(int64(seconds)) {
		return fmt.Sprintf("%ds", int64(seconds))
	}
	// Trim trailing zeros from fractional seconds for readability.
	s := strconv.FormatFloat(seconds, 'f', -1, 64)
	return s + "s"
}
