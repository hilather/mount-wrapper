package convert_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

const sampleSolidList = `
Listing archive: /data/solid.7z

--
Path = /data/solid.7z
Type = 7z
Physical Size = 1234
Headers Size = 122
Method = LZMA2:12
Solid = +
Blocks = 1

----------
Path = a.txt
Size = 10
Packed Size = 20
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = 12345678
Encrypted = -
Method = LZMA2:12
Block = 0

Path = b.txt
Size = 10
Packed Size = 
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = 87654321
Encrypted = -
Method = LZMA2:12
Block = 0
`

const sampleNonSolidList = `
Listing archive: /data/store.7z

--
Path = /data/store.7z
Type = 7z
Physical Size = 500
Headers Size = 100
Method = Copy
Solid = -
Blocks = 2

----------
Path = a.txt
Size = 10
Packed Size = 10
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = 11111111
Encrypted = -
Method = Copy
Block = 0

Path = b.txt
Size = 10
Packed Size = 10
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = 22222222
Encrypted = -
Method = Copy
Block = 1
`

const sampleNestedList = `
Listing archive: /data/outer.7z

--
Path = /data/outer.7z
Type = 7z
Physical Size = 999
Headers Size = 80
Method = LZMA2:12
Solid = -
Blocks = 1

----------
Path = docs/readme.txt
Size = 5
Packed Size = 12
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = abcdef00
Encrypted = -
Method = LZMA2:12
Block = 0

Path = nested/inner.7z
Size = 100
Packed Size = 80
Modified = 2020-01-01 00:00:00
Attributes = A
CRC = deadbeef
Encrypted = -
Method = LZMA2:12
Block = 0
`

func TestParse7zListNeedsFlatten_Solid(t *testing.T) {
	t.Parallel()
	if !convert.Parse7zListNeedsFlatten(sampleSolidList) {
		t.Fatal("expected solid=true")
	}
}

func TestParse7zListNeedsFlatten_NonSolid(t *testing.T) {
	t.Parallel()
	if convert.Parse7zListNeedsFlatten(sampleNonSolidList) {
		t.Fatal("expected non-solid=false")
	}
}

func TestParse7zListNeedsFlatten_Nested(t *testing.T) {
	t.Parallel()
	if !convert.Parse7zListNeedsFlatten(sampleNestedList) {
		t.Fatal("expected nested .7z → true")
	}
}

func TestParse7zListNeedsFlatten_EmptyAndNoise(t *testing.T) {
	t.Parallel()
	if convert.Parse7zListNeedsFlatten("") {
		t.Fatal("empty")
	}
	if convert.Parse7zListNeedsFlatten("not a 7z listing") {
		t.Fatal("noise")
	}
	// Archive path alone (before ----------) must not count as nested.
	onlyArchive := `
--
Path = /data/outer.7z
Type = 7z
Solid = -
----------
Path = a.txt
Size = 1
`
	if convert.Parse7zListNeedsFlatten(onlyArchive) {
		t.Fatal("archive Path alone")
	}
}

func TestParse7zListNeedsFlatten_CRLF(t *testing.T) {
	t.Parallel()
	crlf := strings.ReplaceAll(sampleSolidList, "\n", "\r\n")
	if !convert.Parse7zListNeedsFlatten(crlf) {
		t.Fatal("crlf solid")
	}
}

func TestProbe7zNeedsFlatten_FakeList(t *testing.T) {
	t.Parallel()
	var sawArgs []string
	list := func(bin string, args []string, cwd string) (string, error) {
		sawArgs = append([]string{bin}, args...)
		return sampleSolidList, nil
	}
	if !convert.Probe7zNeedsFlatten("/usr/bin/7z", "/tmp/a.7z", list) {
		t.Fatal("probe solid")
	}
	if len(sawArgs) < 3 || sawArgs[1] != "l" || sawArgs[2] != "-slt" {
		t.Fatalf("args=%v", sawArgs)
	}
	// Non-.7z
	if convert.Probe7zNeedsFlatten("7z", "/tmp/a.zip", list) {
		t.Fatal("zip")
	}
	// Empty output → false
	empty := func(string, []string, string) (string, error) { return "", errors.New("fail") }
	if convert.Probe7zNeedsFlatten("7z", "/tmp/a.7z", empty) {
		t.Fatal("empty+err")
	}
	// Error but useful stdout still parsed
	warn := func(string, []string, string) (string, error) {
		return sampleNestedList, errors.New("warning")
	}
	if !convert.Probe7zNeedsFlatten("7z", "/tmp/a.7z", warn) {
		t.Fatal("warn with nested list")
	}
}

func TestCLIFlattenNeeded_Unavailable(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_7z_bin":      "/no/such/7z-binary-for-tests",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	opts := convert.ResolveOptions{
		SearchPathDisabled: true,
		IsExecutable:       func(string) bool { return false },
	}
	fn := convert.CLIFlattenNeeded(cfg, opts, nil)
	if fn == nil {
		t.Fatal("nil fn")
	}
	if fn(filepath.Join(t.TempDir(), "a.7z")) {
		t.Fatal("unavailable 7z must be false")
	}
}

func TestCLIFlattenNeeded_WithFakeList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Fake "executable" path that passes IsExecutable.
	fakeBin := filepath.Join(dir, "7z")
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_7z_bin":      fakeBin,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	opts := convert.ResolveOptions{
		SearchPathDisabled: true,
		IsExecutable:       func(p string) bool { return p == fakeBin },
	}
	list := func(bin string, args []string, cwd string) (string, error) {
		if bin != fakeBin {
			t.Fatalf("bin=%q", bin)
		}
		return sampleSolidList, nil
	}
	fn := convert.CLIFlattenNeeded(cfg, opts, list)
	archive := filepath.Join(dir, "solid.7z")
	if !fn(archive) {
		t.Fatal("expected true from solid list")
	}
	// Non-solid
	listNS := func(string, []string, string) (string, error) { return sampleNonSolidList, nil }
	fnNS := convert.CLIFlattenNeeded(cfg, opts, listNS)
	if fnNS(archive) {
		t.Fatal("non-solid")
	}
}

func TestDefaultFlattenNeeded_ScopeGate(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "nested",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if convert.DefaultFlattenNeeded(cfg, convert.ResolveOptions{}, nil) != nil {
		t.Fatal("nested scope → nil probe")
	}
	cfg.Convert7zScope = config.Convert7zScopeFlatten
	// 7z may or may not be on PATH; function non-nil when flatten + nonsolid.
	fn := convert.DefaultFlattenNeeded(cfg, convert.ResolveOptions{
		SearchPathDisabled: true,
		IsExecutable:       func(string) bool { return false },
	}, nil)
	if fn == nil {
		t.Fatal("expected non-nil (false) probe when flatten+nonsolid")
	}
	// Always false when 7z missing.
	if fn("/tmp/x.7z") {
		t.Fatal("missing 7z")
	}
	cfg.Convert7zNonsolid = false
	if convert.DefaultFlattenNeeded(cfg, convert.ResolveOptions{}, nil) != nil {
		t.Fatal("nonsolid off → nil")
	}
}

func TestShouldFlattenConvert_WithCLIProbe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.7z")
	// File must exist for ShouldFlattenConvert.
	if err := writeEmpty(archive); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(dir, "7z")
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
		"convert_7z_bin":      fakeBin,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	opts := convert.ResolveOptions{
		SearchPathDisabled: true,
		IsExecutable:       func(p string) bool { return p == fakeBin },
	}
	probe := convert.CLIFlattenNeeded(cfg, opts, func(string, []string, string) (string, error) {
		return sampleSolidList, nil
	})
	if !convert.ShouldFlattenConvert(cfg, archive, probe) {
		t.Fatal("should flatten with solid probe")
	}
	probeNS := convert.CLIFlattenNeeded(cfg, opts, func(string, []string, string) (string, error) {
		return sampleNonSolidList, nil
	})
	if convert.ShouldFlattenConvert(cfg, archive, probeNS) {
		t.Fatal("non-solid should not flatten")
	}
}

func writeEmpty(path string) error {
	return os.WriteFile(path, []byte("x"), 0o644)
}
