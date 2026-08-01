package convert_test

import (
	"testing"

	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestLimitReached(t *testing.T) {
	t.Parallel()
	cases := []struct {
		active, limit int
		want          bool
	}{
		{0, 0, false},
		{5, 0, false},
		{0, 1, false},
		{1, 1, true},
		{2, 1, true},
		{1, 2, false},
		{2, 2, true},
		{0, -1, false},
	}
	for _, tc := range cases {
		if got := convert.LimitReached(tc.active, tc.limit); got != tc.want {
			t.Errorf("LimitReached(%d,%d)=%v want %v", tc.active, tc.limit, got, tc.want)
		}
	}
}

func TestSlotsAvailable(t *testing.T) {
	t.Parallel()
	if n := convert.SlotsAvailable(0, 0); n < 1000 {
		t.Fatalf("unlimited slots=%d", n)
	}
	if n := convert.SlotsAvailable(0, 2); n != 2 {
		t.Fatalf("slots=%d", n)
	}
	if n := convert.SlotsAvailable(2, 2); n != 0 {
		t.Fatalf("slots=%d", n)
	}
	if n := convert.SlotsAvailable(5, 2); n != 0 {
		t.Fatalf("slots=%d", n)
	}
}
