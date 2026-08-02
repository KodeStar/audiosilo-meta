package importer

import (
	"encoding/json"
	"fmt"
)

// conflicts.go makes the importer's CONTRADICTION stream durable.
//
// The guards themselves are old (recordingContradicts in enrich.go): a row whose
// runtime or release date disagrees with the record its ASIN matched is refused
// whole, warned about, and - on a user-library run - counted as
// Summary.Conflicts. What none of that produced was a record anybody could act
// on later. A warning line is prose in a run's stdout, and the counter is a
// number; neither survives the run, and neither says WHICH value the catalogue
// holds or what the source stated. So a genuinely wrong recorded fact (a
// recording stored at 81 minutes that the dump and its own chapter table both
// put at 492) surfaced only when a human happened to trip over it.
//
// The worklist is that missing record: one NDJSON line per refused
// contradiction, appended as it is detected, carrying both values and enough
// addressing to open the record. It is pure observability - the run behaves
// exactly as it did, the warnings are unchanged, and nothing in the import
// branches on it - so a maintainer can run a wave with `--conflicts` and sort
// the result afterwards instead of reading a million lines of stdout live.
//
// Two deliberate scoping decisions:
//
//   - it records EVERY contradiction the guard refuses, in every trust tier.
//     Summary.Conflicts counts only a user-library run's, because that counter
//     is the intake bot's "a person disagreed with what we hold" signal and a
//     bulk mirror disagreeing is expected source noise. The worklist is read by
//     a maintainer looking for wrong DATA, and the mirror's disagreements are
//     precisely where the spider-man runtime was hiding.
//   - the emission point is where the guard fires, not a later re-derivation
//     from the warning strings. The fields are the values the comparison already
//     had in hand, so the row cannot drift from the decision it describes.

// Conflict is one refused contradiction, as written to the worklist. It is
// exported so a consumer decodes a worklist with the struct that wrote it,
// rather than re-spelling the field names.
//
// Recorded/Stated are `any` because the fields the guard compares are not one
// type: a runtime is a number and a release date is a string. Each is rendered
// as what it is, so a consumer's `recorded > stated` on a runtime works without
// parsing prose.
type Conflict struct {
	// Run is the planning mode that detected it: "create", "enrich" or
	// "recordings-only".
	Run string `json:"run"`
	// ASIN is the row's own identifier - the one that matched the record, on
	// every path that can reach a contradiction.
	ASIN string `json:"asin"`
	// Work / Recording address the record whose value was kept. A recording has
	// no identity of its own, so it takes both (see RecRef).
	Work      string `json:"work"`
	Recording string `json:"recording"`
	// Field is the schema field the two disagree on: "runtime_min" or
	// "release_date".
	Field string `json:"field"`
	// Recorded is the value the catalogue holds (and kept - first writer wins);
	// Stated is what the refused row said.
	Recorded any `json:"recorded"`
	Stated   any `json:"stated"`
	// SourceType is the run's provenance type (libex-import,
	// openaudible-import, ...), so a worklist accumulated across several runs
	// still says who disagreed.
	SourceType string `json:"source_type"`
	// DetectedAt is the run's import date - the same stamp its records carry,
	// rather than a wall clock read here, so a run and the worklist it produced
	// are dated identically.
	DetectedAt string `json:"detected_at"`
}

// recordConflict appends one row to the run's worklist, if the run was given
// one. A run without one pays a nil check and nothing else, which is what keeps
// the flag purely additive.
//
// Every row is written with ONE Write on the caller's writer (the CLI hands over
// the *os.File itself, deliberately unbuffered): a wave that is killed part-way
// keeps every conflict it had already seen, which is the point of appending as
// they are found rather than dumping a list at the end.
func (p *planner) recordConflict(b sourceBook, ref RecRef, field string, recorded, stated any) {
	if p.conflicts == nil {
		return
	}
	line, err := json.Marshal(Conflict{
		Run:        p.mode.runName(),
		ASIN:       NormalizeASIN(b.str("asin")),
		Work:       ref.Work,
		Recording:  ref.Rec,
		Field:      field,
		Recorded:   recorded,
		Stated:     stated,
		SourceType: p.sourceType,
		DetectedAt: p.importDate,
	})
	if err != nil {
		// Unreachable for the field types above; handled rather than ignored so
		// a later field of some richer type cannot fail silently.
		p.noteConflictLogFailure(err)
		return
	}
	if _, err := p.conflicts.Write(append(line, '\n')); err != nil {
		p.noteConflictLogFailure(err)
	}
}

// noteConflictLogFailure reports a worklist that stopped taking writes and gives
// up on it, so a full disk costs the run one warning rather than one per
// remaining conflict.
//
// It deliberately does not fail the run: the worklist is observability over an
// import that is otherwise complete and validated, and discarding a landed
// tranche because its side-channel filled up would be the worse outcome. The
// warning is what makes the loss visible - a silently truncated worklist would
// read as "no more conflicts".
func (p *planner) noteConflictLogFailure(err error) {
	p.conflicts = nil
	p.summary.Warnings = append(p.summary.Warnings,
		fmt.Sprintf("conflict worklist: %v; the remaining conflicts of this run were not recorded", err))
}
