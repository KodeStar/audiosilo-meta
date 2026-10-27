package main

import "testing"

// TestEnvFloat pins the environment fallback the rate-limit flags read: an unset
// variable is the default, a number is taken, and a typo is reported and falls
// back rather than becoming a zero (which would turn the limit off).
func TestEnvFloat(t *testing.T) {
	const name = "METASERVE_TEST_ENV_FLOAT"
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"", 10},
		{"2.5", 2.5},
		{"0", 0},
		{"ten", 10},
	} {
		t.Setenv(name, tc.raw)
		if got := envFloat(name, 10); got != tc.want {
			t.Errorf("envFloat(%q) = %g, want %g", tc.raw, got, tc.want)
		}
	}
}

// TestEnvInt pins the burst's environment fallback: a whole number is taken
// (0 included, which serve.New then refuses with the limit on), and a typo or
// a fraction is reported and falls back rather than being truncated.
func TestEnvInt(t *testing.T) {
	const name = "METASERVE_TEST_ENV_INT"
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 400},
		{"50", 50},
		{"0", 0},
		{"2.5", 400},
		{"lots", 400},
	} {
		t.Setenv(name, tc.raw)
		if got := envInt(name, 400); got != tc.want {
			t.Errorf("envInt(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
