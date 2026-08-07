//go:build race

package check

// raceEnabled reports whether this test binary was built with -race. Go has no
// runtime predicate for it, so it is a build-tagged constant pair (see
// race_off_test.go). Its one use is TestRealDataTree, which costs minutes under
// the detector and adds no coverage the fixtures do not already give.
const raceEnabled = true
