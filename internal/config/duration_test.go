package config

import (
	"testing"
)

func TestParseDuration_numbers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want float64
	}{
		{60, 60},
		{1.5, 1.5},
		{int64(90), 90},
		{float32(2.5), 2.5},
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.in, "d")
		if err != nil {
			t.Fatalf("ParseDuration(%v): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseDuration(%v)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseDuration_strings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{"60", 60},
		{"60s", 60},
		{" 90 S ", 90},
		{"30m", 1800},
		{"24h", 86400},
		{"7d", 7 * 86400},
		{"500ms", 0.5},
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.in, "d")
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseDuration(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseDuration_invalid(t *testing.T) {
	t.Parallel()
	if _, err := ParseDuration("soon", "d"); err == nil {
		t.Fatal("expected error for invalid duration string")
	}
	if _, err := ParseDuration(true, "d"); err == nil {
		t.Fatal("expected error for boolean")
	}
	if _, err := ParseDuration(-1, "d"); err == nil {
		t.Fatal("expected error for negative")
	}
}

func TestFormatDuration_roundtripCommon(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0s"},
		{86400, "1d"},
		{2 * 86400, "2d"},
		{3600, "1h"},
		{60, "1m"},
		{48 * 3600, "2d"},
		{36 * 3600, "36h"},
		{0.5, "500ms"},
		{45, "45s"},
	}
	for _, tc := range cases {
		if got := FormatDuration(tc.sec); got != tc.want {
			t.Fatalf("FormatDuration(%v)=%q want %q", tc.sec, got, tc.want)
		}
	}
}

func TestParseFormat_roundtrip(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"0s", "500ms", "45s", "1m", "2h", "3d", "36h"} {
		sec, err := ParseDuration(s, "d")
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		out := FormatDuration(sec)
		sec2, err := ParseDuration(out, "d")
		if err != nil {
			t.Fatalf("reparse %q: %v", out, err)
		}
		if sec != sec2 {
			t.Fatalf("roundtrip %q -> %q: %v vs %v", s, out, sec, sec2)
		}
	}
}
