package repair

import "github.com/kodestar/audiosilo-meta/internal/rawentry"

// entry.go is this package's ONE door onto internal/rawentry, the raw-member
// pack-entry editor and the by-value union rules a merge folds two records together
// with. Nothing about those rules lives here - see that package for why an entry is
// edited as bytes rather than through the typed structs, and why each union key is the
// key pkg/check enforces uniqueness on.
//
// The alias is a local NAME, not a second definition: `entry` is the noun this
// package's prose and every signature in it uses, and an alias is the same type rather
// than a wrapper with methods of its own. Everything else is called through the
// package, so there is one implementation and no delegation to keep in step.
type entry = rawentry.Obj
