//go:build race

package recorddiff

// raceEnabled reports whether this test binary was built with -race. Go has no
// runtime predicate for it, so it is a build-tagged constant pair (see
// race_off_test.go). Its one use is TestAgainstTheRealCheckout, which bounds
// how much of the last commit it diffs under the detector.
const raceEnabled = true
