//go:build !race

package importer

// raceEnabled is false in an ordinary test binary - see race_on_test.go.
const raceEnabled = false
