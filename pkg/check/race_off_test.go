//go:build !race

package check

// raceEnabled is false in an ordinary test binary - see race_on_test.go.
const raceEnabled = false
