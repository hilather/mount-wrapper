package config

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

// LogLevelEnv overrides config log_level when set to a valid LogLevels name.
// Applied at serve start and on config reload (env wins while set).
const LogLevelEnv = "MOUNT_WRAPPER_LOG_LEVEL"

var (
	logLevelOnce sync.Once
	logLevelVar  *slog.LevelVar
)

// ensureLogLevelVar installs a LevelVar-backed default slog handler once so
// ApplyLogLevel can change verbosity without rebuilding handlers.
func ensureLogLevelVar() *slog.LevelVar {
	logLevelOnce.Do(func() {
		logLevelVar = new(slog.LevelVar)
		logLevelVar.Set(slog.LevelInfo)
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevelVar})
		slog.SetDefault(slog.New(h))
	})
	return logLevelVar
}

// slogLevelFromName maps config/env log_level names to slog levels.
// CRITICAL is above ERROR (Python logging parity).
func slogLevelFromName(name string) (slog.Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DEBUG":
		return slog.LevelDebug, true
	case "INFO":
		return slog.LevelInfo, true
	case "WARNING", "WARN":
		return slog.LevelWarn, true
	case "ERROR":
		return slog.LevelError, true
	case "CRITICAL":
		return slog.LevelError + 4, true
	default:
		return 0, false
	}
}

// NormalizeLogLevel returns the canonical LogLevels name, or "" if invalid.
func NormalizeLogLevel(name string) string {
	n := strings.ToUpper(strings.TrimSpace(name))
	if n == "WARN" {
		n = "WARNING"
	}
	if _, ok := LogLevels[n]; ok {
		return n
	}
	return ""
}

// EffectiveLogLevel returns MOUNT_WRAPPER_LOG_LEVEL when set and valid,
// otherwise cfg.LogLevel, otherwise "INFO". Invalid env values are ignored
// (caller may log a warning via ApplyEffectiveLogLevel).
func EffectiveLogLevel(cfg *Config) string {
	if env := strings.TrimSpace(os.Getenv(LogLevelEnv)); env != "" {
		if n := NormalizeLogLevel(env); n != "" {
			return n
		}
	}
	if cfg != nil {
		if n := NormalizeLogLevel(cfg.LogLevel); n != "" {
			return n
		}
	}
	return "INFO"
}

// EnvLogLevelInvalid reports whether LogLevelEnv is set to a non-empty but
// unrecognized value (ignored; config/default is used instead).
func EnvLogLevelInvalid() bool {
	env := strings.TrimSpace(os.Getenv(LogLevelEnv))
	if env == "" {
		return false
	}
	return NormalizeLogLevel(env) == ""
}

// ApplyLogLevel installs the process slog default handler (once) and sets the
// active level. Returns the canonical level name actually applied.
// Unknown names fall back to INFO.
func ApplyLogLevel(name string) string {
	canonical := NormalizeLogLevel(name)
	if canonical == "" {
		canonical = "INFO"
	}
	level, ok := slogLevelFromName(canonical)
	if !ok {
		level = slog.LevelInfo
		canonical = "INFO"
	}
	ensureLogLevelVar().Set(level)
	return canonical
}

// ApplyEffectiveLogLevel applies EffectiveLogLevel(cfg) to slog.
// Returns the level name applied.
func ApplyEffectiveLogLevel(cfg *Config) string {
	return ApplyLogLevel(EffectiveLogLevel(cfg))
}
