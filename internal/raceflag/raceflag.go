// Package raceflag says whether the binary was built with -race. Go has no
// runtime predicate for it, so it is a build-tagged constant pair (race_on.go,
// race_off.go), held once here rather than copied into every package that asks.
//
// Its users are the REAL-DATA tests: pkg/check's TestRealDataTree,
// pkg/canonical's TestRealDataCanonical, internal/importer's real-data walk and
// internal/repair's TestRealTreeDryRunComposesAPlan skip under the detector -
// each costs minutes there and adds no coverage the fixture suites do not
// already give - and internal/recorddiff's TestAgainstTheRealCheckout diffs a
// bounded sample of the last commit instead of all of it. The whole suite runs
// without -race in real-data.yml.
package raceflag
