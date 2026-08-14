//go:build race

package repair

// raceEnabled reports whether this test binary was built with -race. Go has no runtime
// predicate for it, so it is a build-tagged constant pair (see race_off_test.go). Its
// one use is TestRealTreeDryRunComposesAPlan, which loads the whole catalogue and costs
// minutes under the detector while adding no coverage the fixtures do not already give -
// the same trade pkg/check's TestRealDataTree makes.
const raceEnabled = true
