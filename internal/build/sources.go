package build

// sources.go names the data roots one artifact is compiled from, and makes the
// one choice metabuild's --community flag exists to make.
//
// The community layer is moving to a repository of its own, which turns the
// database into two checkouts; a release keeps being one artifact off one
// builder, so the builder is where the two meet. Everything about HOW they meet
// - the profiles, the cross-tree rules, the retired-key resolution - is
// pkg/check's, in compose.go, because those are validation rules over a
// catalogue and pkg/check owns both the rules and the paths a problem is
// reported against. What is left here is which door to walk through, which is
// exactly what a flag decides.

import "github.com/kodestar/audiosilo-meta/pkg/check"

// Sources are the data roots a build reads.
//
// Community is empty for a single tree holding the whole database, which is what
// this repository is until the split lands - and that case is not "compose with
// nothing", it is the load metabuild has always done, so the artifact is
// byte-identical to every one built before this field existed. Setting it says
// "there IS a community layer, here", which is why a root that holds none is a
// hard error rather than an empty compose (check.LoadComposed): omitting the
// field stays the way to build the core alone.
type Sources struct {
	// Data is the primary data root. It holds the whole database (pack.ProfileAll)
	// when Community is empty, and the CC0 core alone (pack.ProfileCore) when it
	// is set.
	Data string
	// Community is the second root, holding the works-community family alone
	// (pack.ProfileCommunity). Empty means one tree.
	Community string
}

// Load validates the sources and returns the catalogue to build from, with the
// cross-tree rules already run when there are two roots (see check.LoadComposed).
// A caller builds from Result.Catalog only when Result.OK().
func Load(s Sources) check.Result {
	if s.Community == "" {
		return check.Load(s.Data)
	}
	return check.LoadComposed(s.Data, s.Community)
}
