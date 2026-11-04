//go:build !race

package raceflag

// Enabled reports whether this binary was built with -race - see raceflag.go.
const Enabled = false
