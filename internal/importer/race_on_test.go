//go:build race

package importer

// raceEnabled reports whether this test binary was built with -race. Go has no
// runtime predicate for it, so it is a build-tagged constant pair (see
// race_off_test.go). Its one use is walkEntries, the real-data walk under
// realdata_slug_test.go, which is sequential and costs minutes under the
// detector - the same trade pkg/check's TestRealDataTree makes.
const raceEnabled = true
