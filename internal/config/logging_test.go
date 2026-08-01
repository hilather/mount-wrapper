package config_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
)

func TestNormalizeLogLevel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"DEBUG", "DEBUG"},
		{"debug", "DEBUG"},
		{" INFO ", "INFO"},
		{"WARNING", "WARNING"},
		{"warn", "WARNING"},
		{"ERROR", "ERROR"},
		{"CRITICAL", "CRITICAL"},
		{"VERBOSE", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := config.NormalizeLogLevel(tc.in); got != tc.want {
			t.Errorf("NormalizeLogLevel(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEffectiveLogLevel_envOverridesConfig(t *testing.T) {
	// Env is process-global; do not t.Parallel.
	t.Setenv(config.LogLevelEnv, "DEBUG")
	cfg := &config.Config{LogLevel: "ERROR"}
	if got := config.EffectiveLogLevel(cfg); got != "DEBUG" {
		t.Fatalf("env override: got %q", got)
	}
	t.Setenv(config.LogLevelEnv, "")
	if got := config.EffectiveLogLevel(cfg); got != "ERROR" {
		t.Fatalf("config only: got %q", got)
	}
	if got := config.EffectiveLogLevel(nil); got != "INFO" {
		t.Fatalf("nil cfg: got %q", got)
	}
}

func TestEffectiveLogLevel_invalidEnvIgnored(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "VERBOSE")
	cfg := &config.Config{LogLevel: "WARNING"}
	if got := config.EffectiveLogLevel(cfg); got != "WARNING" {
		t.Fatalf("invalid env should fall through: got %q", got)
	}
	if !config.EnvLogLevelInvalid() {
		t.Fatal("expected EnvLogLevelInvalid")
	}
	t.Setenv(config.LogLevelEnv, "INFO")
	if config.EnvLogLevelInvalid() {
		t.Fatal("valid env should not be invalid")
	}
}

func TestApplyLogLevel_setsSlog(t *testing.T) {
	// Sequential: mutates process slog default.
	_ = config.ApplyLogLevel("DEBUG")
	if !slog.Default().Enabled(nil, slog.LevelDebug) {
		t.Fatal("DEBUG should enable debug logs")
	}
	_ = config.ApplyLogLevel("ERROR")
	if slog.Default().Enabled(nil, slog.LevelInfo) {
		t.Fatal("ERROR should disable info")
	}
	if !slog.Default().Enabled(nil, slog.LevelError) {
		t.Fatal("ERROR should enable error")
	}
	// Reset so other packages' tests are not starved of INFO.
	_ = config.ApplyLogLevel("INFO")
	_ = os.Unsetenv(config.LogLevelEnv)
}

func TestApplyEffectiveLogLevel(t *testing.T) {
	t.Setenv(config.LogLevelEnv, "WARNING")
	applied := config.ApplyEffectiveLogLevel(&config.Config{LogLevel: "DEBUG"})
	if applied != "WARNING" {
		t.Fatalf("applied=%q", applied)
	}
	if slog.Default().Enabled(nil, slog.LevelInfo) {
		t.Fatal("WARNING should disable info")
	}
	t.Setenv(config.LogLevelEnv, "")
	_ = config.ApplyLogLevel("INFO")
}

func TestLogLevelEnvConstant(t *testing.T) {
	t.Parallel()
	if config.LogLevelEnv != "MOUNT_WRAPPER_LOG_LEVEL" {
		t.Fatalf("LogLevelEnv=%q", config.LogLevelEnv)
	}
}
