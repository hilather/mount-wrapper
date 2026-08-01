package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
)

// TestSettingsSchemaMatchesPublicKeys guards SPA settings-schema.ts drift
// against config.PublicKeys() (Appendix D / D11 hand-written types).
// When adding a public YAML key, update web/src/lib/settings-schema.ts too.
func TestSettingsSchemaMatchesPublicKeys(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	schemaPath := filepath.Join(root, "web/src/lib/settings-schema.ts")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`key:\s*'([^']+)'`)
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) < 50 {
		t.Fatalf("too few schema keys in %s: %d", schemaPath, len(matches))
	}
	seen := map[string]struct{}{}
	var spa []string
	for _, m := range matches {
		k := m[1]
		if _, ok := seen[k]; ok {
			t.Fatalf("duplicate settings-schema key %q", k)
		}
		seen[k] = struct{}{}
		spa = append(spa, k)
	}
	sort.Strings(spa)

	goKeys := config.PublicKeys()
	if len(spa) != len(goKeys) {
		t.Fatalf("settings-schema keys=%d PublicKeys=%d\nonly schema: %v\nonly Go: %v",
			len(spa), len(goKeys), setDiff(spa, goKeys), setDiff(goKeys, spa))
	}
	for i := range spa {
		if spa[i] != goKeys[i] {
			t.Fatalf("key mismatch at %d: schema=%q PublicKeys=%q\nonly schema: %v\nonly Go: %v",
				i, spa[i], goKeys[i], setDiff(spa, goKeys), setDiff(goKeys, spa))
		}
	}
}

func setDiff(a, b []string) []string {
	bs := map[string]struct{}{}
	for _, k := range b {
		bs[k] = struct{}{}
	}
	var out []string
	for _, k := range a {
		if _, ok := bs[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"(none)"}
	}
	if len(out) > 20 {
		return append(out[:20], "…+"+strconv.Itoa(len(out)-20))
	}
	return out
}
