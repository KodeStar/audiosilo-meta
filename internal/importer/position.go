package importer

import "github.com/kodestar/audiosilo-meta/pkg/model"

// position.go re-exports the SERIES-POSITION primitive under the names this package's
// callers (internal/audit, internal/repair, internal/issueform) have always used. The
// rule, and why its ORDER is load-bearing, are pkg/model's (model.PositionSpan).

// PositionSpan is model.PositionSpan.
func PositionSpan(pos string) (span [2]float64, ok bool) { return model.PositionSpan(pos) }

// PositionRange is model.PositionRange.
func PositionRange(pos string) (lo, hi float64, isRange bool) { return model.PositionRange(pos) }

// SameSlot is model.SameSlot.
func SameSlot(a, b string) bool { return model.SameSlot(a, b) }
