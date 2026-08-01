package config_test

import (
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
)

// FuzzLoadText ensures YAML config parsing does not panic on arbitrary input.
// Invalid YAML / validation failures are expected; only panics fail the fuzz.
func FuzzLoadText(f *testing.F) {
	seeds := []string{
		``,
		`version: 1`,
		`version: 1
source_dirs: ["/tmp"]
`,
		`not: a: valid: yaml: [`,
		`[]`,
		`null`,
		`version: "boom"`,
		`poll_interval_seconds: -1`,
		`web_port: 99999`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		// Discard result; we only care that LoadText does not panic.
		_, _ = config.LoadText(text, "fuzz.yaml")
	})
}
