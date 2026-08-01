package convert_test

import (
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestConvertSpaceRequired(t *testing.T) {
	t.Parallel()
	// archive*2 + min + overhead
	if got := convert.ConvertSpaceRequired(100, 10, 5); got != 215 {
		t.Fatalf("got %d", got)
	}
	if got := convert.ConvertSpaceRequired(-1, -1, -1); got != 0 {
		t.Fatalf("negatives → %d", got)
	}
}

func TestCheckConvertSpace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1000, true
	})
	defer restore()

	if err := convert.CheckConvertSpace(dir, 100, 0, 0); err != nil {
		// need 200; free 1000 → ok
		t.Fatalf("unexpected: %v", err)
	}
	if err := convert.CheckConvertSpace(dir, 600, 0, 0); err == nil {
		// need 1200; free 1000 → fail
		t.Fatal("expected insufficient space")
	} else if !strings.Contains(err.Error(), "insufficient_space_for_convert") {
		t.Fatalf("err=%v", err)
	}

	// Probe fail → allow
	restore2 := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 0, false
	})
	defer restore2()
	if err := convert.CheckConvertSpace(dir, 1e12, 0, 0); err != nil {
		t.Fatalf("probe fail should allow: %v", err)
	}
}

func TestZipRepackSpaceRequired(t *testing.T) {
	t.Parallel()
	if got := convert.ZipRepackSpaceRequired(100, 10, 5); got != 115 {
		t.Fatalf("got %d", got)
	}
}

func TestCheckZipRepackSpace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 50, true
	})
	defer restore()
	if err := convert.CheckZipRepackSpace(dir, 100, 0, 0); err == nil {
		t.Fatal("expected insufficient")
	}
	if err := convert.CheckZipRepackSpace(dir, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
}
