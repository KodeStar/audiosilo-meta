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
