package config_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
)

func TestLoadPackagingExamples(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, rel := range []string{
		"packaging/examples/config.yaml.example",
		"packaging/examples/config.yaml.macos.example",
		"packaging/examples/config.debug.yaml.example",
	} {
		p := filepath.Join(root, rel)
		cfg, err := config.Load(p)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if cfg.Version != 1 {
			t.Fatalf("%s: version %d", rel, cfg.Version)
		}
	}
}
