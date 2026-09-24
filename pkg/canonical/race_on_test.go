//go:build race

package canonical

// raceEnabled reports whether this test binary was built with -race. Go has no
// runtime predicate for it, so it is a build-tagged constant pair (see
// race_off_test.go). Its one use is TestRealDataCanonical, which walks the whole
// data tree - minutes under the detector for no concurrency coverage, since
// CheckTree is sequential - the same trade pkg/check's TestRealDataTree makes.
const raceEnabled = true
