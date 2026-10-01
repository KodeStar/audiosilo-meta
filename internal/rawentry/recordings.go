package rawentry

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RuntimesCompatible accepts unknown runtimes or known runtimes within 10% of the larger.
func RuntimesCompatible(a, b int) bool {
	if a <= 0 || b <= 0 {
		return true
	}
	hi, lo := a, b
	if lo > hi {
		hi, lo = lo, hi
	}
	return float64(hi-lo) <= 0.10*float64(hi)
}

// RecordingMove describes the merge or intact move performed by MoveRecording.
type RecordingMove struct {
	ID     string
	Merged bool
	Why    string
	Lost   []RecordingLoss
}

// MoveRecording merges into the first same-production candidate, or moves intact
// to key (asking freeKey only on collision). Callers choose eligible siblings and
// their re-key limits; the production and field-merge rules are shared.
// A false result means freeKey exhausted its search, with recs unchanged.
func MoveRecording(recs map[string]Obj, rec Obj, target, key string, candidates []string, freeKey func() (string, bool)) (RecordingMove, bool) {
	result := RecordingMove{ID: key}
	moved := rec.Clone()
	moved.Set("work", target)
	for _, id := range candidates {
		sibling, held := recs[id]
		if !held {
			continue
		}
		why, same := SameProduction(sibling, moved)
		result.Why = why
		if same {
			merged, lost := MergeRecordings(sibling, moved)
			recs[id] = merged
			return RecordingMove{ID: id, Merged: true, Why: why, Lost: lost}, true
		}
	}
	if _, held := recs[key]; held {
		var ok bool
		result.ID, ok = freeKey()
		if !ok {
			return result, false
		}
		moved.Set("id", result.ID)
	}
	recs[result.ID] = moved
	return result, true
}

// AbridgedConflict treats an absent flag as unabridged.
func AbridgedConflict(a, b *bool) bool { return (a != nil && *a) != (b != nil && *b) }

func sameStringSet(a, b []string) bool {
	set := func(values []string) map[string]bool {
		out := map[string]bool{}
		for _, v := range values {
			out[v] = true
		}
		return out
	}
	aa, bb := set(a), set(b)
	if len(aa) != len(bb) {
		return false
	}
	for v := range aa {
		if !bb[v] {
			return false
		}
	}
	return true
}

// SameProduction compares narrator sets, runtime and abridgement, and says why
// in either direction. Callers select eligible siblings (including language).
//
// The importer calls these same runtime and abridgement primitives:
// this pass asks the very question the ASIN-merge guard asks, and the first draft
// restated both halves and got both boundaries wrong - "the larger is at most 1.1x the
// smaller" instead of within-10%-of-the-larger, and an abridged refusal that needed
// BOTH sides to state the flag, where the importer reads an ABSENT flag as unabridged
// so an abridgement never folds into an unstated recording. Where the evidence does not
// agree the mover is re-keyed, which loses nothing, so the conservative branch is also
// the cheap one.
func SameProduction(a, b Obj) (string, bool) {
	na, nb := a.Strs("narrators"), b.Strs("narrators")
	if !sameStringSet(na, nb) {
		return fmt.Sprintf("narrators differ: [%s] vs [%s]", strings.Join(na, ", "), strings.Join(nb, ", ")), false
	}
	ra, _ := a.IntAt("runtime_min")
	rb, _ := b.IntAt("runtime_min")
	if !RuntimesCompatible(ra, rb) {
		return fmt.Sprintf("runtimes %d and %d min are more than 10%% apart", ra, rb), false
	}
	if AbridgedConflict(a.BoolPtr("abridged"), b.BoolPtr("abridged")) {
		return "the abridged flags disagree (an absent flag reads as unabridged, so an abridgement never folds into one)", false
	}
	return "same narrators" + runtimeEvidence(ra, rb), true
}

// runtimeEvidence renders what the runtimes contributed to a SameProduction answer, so
// a merge note says whether they agreed or were simply unstated. A runtime of 0 or less
// is "unknown", which is the same reading RuntimesCompatible gives it.
func runtimeEvidence(ra, rb int) string {
	switch {
	case ra > 0 && rb > 0:
		return fmt.Sprintf(", runtimes %d and %d min within 10%%", ra, rb)
	case ra > 0 || rb > 0:
		return ", one runtime stated and the other unstated"
	default:
		return ", no runtime stated on either"
	}
}

// RecordingLoss describes one choice a merge made. Kept and Dropped are
// human-readable values (chapter counts for timelines); DroppedRaw preserves the
// discarded bytes for relocation's notes. Either side may lose a chapter list.
type RecordingLoss struct {
	Field      string
	Kept       string
	Dropped    string
	DroppedRaw json.RawMessage
}

// MergeRecordings folds mover into keep: identifiers and provenance unioned, every field the
// keeper does not state filled from the mover, and every value that could not survive
// REPORTED rather than silently discarded.
//
// The chapter list is the one field chosen by CONTENT rather than by side: both recordings
// describe the same production (the same-production evidence is what got them here), so the
// LONGER list is strictly more of the same truth - a keeper with 3 chapters and a mover with
// 42 used to keep the 3. Measured over a 386-merge wave, 4 of 40 dropped lists were richer
// than the one kept, and a full wave discarded about 7,198 chapter entries.
//
// added_at is deliberately not filled, on either record kind. It records when THIS record
// entered the database; the mover's own date belongs to a record that no longer exists, and
// the provenance that dates it survives in the unioned sources[], which is what metabuild
// falls back to when added_at is absent.
func MergeRecordings(keep, mover Obj) (Obj, []RecordingLoss) {
	out := keep.Clone()
	SetListOrDrop(out, "asin", UnionASINs(out.ASINs(), mover.ASINs()))
	SetListOrDrop(out, "isbn", UnionISBNs(out.ISBNs(), mover.ISBNs()))
	out.Set("sources", UnionSources(out.Sources(), mover.Sources()))
	SetListOrDrop(out, "narrators", AppendUnique(out.Strs("narrators"), mover.Strs("narrators")))

	lost := fillRecordingStrings(out, mover, "release_date", "publisher", "cover_url", "language")
	// The tri-state and byte-exact members: an absent abridged is "unknown" and a false one
	// is a statement, and a chapter list is a timeline whose numbers must survive exactly as
	// they were written, so it is moved as BYTES either way.
	for _, field := range []string{"abridged", "runtime_min", "publishers"} {
		if FillAbsentRaw(out, mover, field) {
			continue
		}
		if mover.Has(field) && !sameRawMember(out, mover, field) {
			lost = append(lost, RecordingLoss{Field: field, Kept: factValue(out[field]), Dropped: factValue(mover[field]), DroppedRaw: mover[field]})
		}
	}
	if f, ok := chooseChapters(out, mover); ok {
		lost = append(lost, f)
	}
	return out, lost
}

// chooseChapters keeps the longer of the two chapter lists and reports the choice. A list
// only the mover carries is simply taken (no choice was made); equal lengths keep the
// keeper's, which is arbitrary between two equally rich timelines and is still reported when
// the bytes differ.
func chooseChapters(out, mover Obj) (RecordingLoss, bool) {
	if !mover.Has("chapters") {
		return RecordingLoss{}, false
	}
	if !out.Has("chapters") {
		out.SetRaw("chapters", mover["chapters"])
		return RecordingLoss{}, false
	}
	kept, moved := len(out.Chapters()), len(mover.Chapters())
	f := RecordingLoss{
		Field:      "chapters",
		Kept:       fmt.Sprintf("%d chapters", kept),
		Dropped:    fmt.Sprintf("%d chapters", moved),
		DroppedRaw: mover["chapters"],
	}
	if moved > kept {
		f.DroppedRaw = out["chapters"]
		out.SetRaw("chapters", mover["chapters"])
		f.Kept, f.Dropped = f.Dropped, f.Kept
		return f, true
	}
	if sameRawMember(out, mover, "chapters") {
		return RecordingLoss{}, false // the same timeline written twice
	}
	return f, true
}

// sameRawMember reports whether two entries carry byte-identical values for a member. Both
// sides came off disk through the same canonical renderer, so a byte comparison is a value
// comparison for anything this function is asked about.
func sameRawMember(a, b Obj, field string) bool {
	return string(a[field]) == string(b[field])
}

func fillRecordingStrings(into, from Obj, fields ...string) []RecordingLoss {
	var lost []RecordingLoss
	for _, field := range fields {
		if FillAbsentString(into, from, field) {
			continue // the keeper stated nothing, so nothing was chosen away
		}
		if v := from.Str(field); v != "" && v != into.Str(field) {
			lost = append(lost, RecordingLoss{Field: field, Kept: into.Str(field), Dropped: v, DroppedRaw: from[field]})
		}
	}
	return lost
}

func factValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
