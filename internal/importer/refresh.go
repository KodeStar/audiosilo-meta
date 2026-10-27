package importer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// refresh.go is the POST-RELEASE REFRESH: a recording the bulk mirror
// catalogued from a PREORDER listing is refreshed, not filled, by the first
// released row of one of its own ASINs.
//
// libex lists a book as soon as it is up for preorder, with an estimated length,
// a placeholder cover, often no chapter table and a release date that may still
// move; once the book is out the listing is corrected. Measured on 2026-10-27
// over 40 random recordings catalogued before their release and released by
// 2026-10-20: 12 runtimes differed from libex's current one by more than 2%, 10
// covers were another image, and 330 of the 1,204 such recordings still carried
// no chapter list. Enrichment is fill-absent, so none of it ever followed - the
// runtime alone was corrected, and only within the estimate bound
// (correctEstimate, which the merge still uses).
//
// WHICH record: a PREORDER-ERA one (awaitingRefresh) - bulk-mirror-only, FIRST
// stated before its release date (its earliest dated source; added_at where no
// source is dated), and not yet RESTATED: its FIRST ASIN (asin[0], the one it
// was catalogued under and the only one a refresh is read from) not stated on
// or after the release. That is deliberately NOT
// estimate.go's runtimeEstimated, which asks whether EVERY statement predates
// the release: an ASIN merge of a regional edition after the release stamps a
// new ref dated after it, which ends the runtime's estimate status (and the
// merge corrects the runtime within its bound) but replaced no cover, chapter
// list or date. Measured on main 3c35c2532b at 2026-10-27: 1,302 bulk-mirror
// recordings with an ASIN were first stated before a release that has passed,
// 505 of them still estimates by runtimeEstimated and 801 ended by such a
// post-release stamp alone - the region-fill waves and the sync bot's regional
// attachments - which the estimate predicate would have left unrefreshed for
// good. 36 more carry a restatement already: an enrichment of their first ASIN
// after release under the earlier correctEstimate rule, which corrected the
// runtime and filled only what was absent; they read as refreshed. A record any
// non-mirror source attested (a user library, an intake form, a hand edit
// citing one) is never refreshed, as it is never overwritten by the mirror at
// all.
//
// WHICH row: a bulk-mirror run's ENRICHMENT row (scopeFill) of the recording's
// FIRST ASIN, stating its capture day (updatedAt) no earlier than any statement
// the record holds of that ASIN (redates) - the listing it was catalogued from, the same product, which
// is why no estimate bound reads its runtime as identity. A row of any other
// ASIN on it - a regional edition merged since - is ordinary enrichment: it
// carries its own marketplace's cover and date, and an ASIN merged onto the
// wrong production would overwrite runtime and chapters past a floor that
// cannot see it, so a wrong value is worse than a stale one. It must
// be RELEASED (rowReleased: its date on or before the day it was captured, and
// libex productState AVAILABLE where stated) with a FULL release date (a
// year-only one says too little), and state a CREDIBLE runtime
// (judgeOwnASINRuntime): over the sanity floor, and agreeing within 10% with the
// row's own chapter list, else with the recorded one, where either exists. Its
// stamp must also END the era: the run's day on or after the release date the
// record carries once written (a run dated before it - a sync cycle crossing
// UTC midnight, an explicit older --date - would append a stamp that ends
// nothing, again on every run). A row failing any of that is no refresh: it
// goes down the ordinary path, the runtime half of the contradiction guard
// included, and the record stays preorder-era for a later row.
//
// WHAT it replaces: the refresh is the OVERWRITE posture for four fields -
// runtime_min, cover_url (an https one), chapters (a list that builds; the
// judgement has found it agrees with the runtime, and libex-fill attaches only a
// payload passing acceptChapters; never a THINNER one over a recorded table,
// thinnerChapters) and release_date - written by enrichment's own fill block
// (applyToRecording), with the contradiction guard skipped.
// Narrators, abridged, publisher, asin[], isbn[] and the work stay as
// enrichment leaves them: publisher and ISBNs fill-absent, the rest untouched.
//
// HOW it ends: the refresh always writes, even when the row confirms every
// value, and its stamp is always APPENDED, past the source deduplication
// (restates) - the RESTATEMENT, the row's ASIN dated the run's day, on or after
// the release, which is the marker awaitingRefresh reads. A later row is
// therefore ordinary enrichment again (fill-absent, fully
// guarded), and neither a re-run nor libex-fill --released-preorders selects the
// record a second time. Only a refresh's stamp restates: an ordinary enrichment
// of a preorder-era record is deduplicated, so a row that refreshed nothing
// cannot end the era.
//
// A preorder-era record's RELEASE DATE is estimate evidence, as its runtime is,
// for the one listing it came from (redates): a row of the recording's FIRST
// ASIN, the one it was catalogued under, captured no earlier than the newest
// statement the record holds of that ASIN. The contradiction guard does not
// read the date against such a row, and a FULL date it states replaces it - a
// released row's in the refresh, any other's as a MOVED PREORDER DATE
// (Summary.MovedPreorderDates; nothing else changes, and its stamp is
// deduplicated, so the record stays preorder-era for the released row that
// follows). A regional edition merged since carries its own marketplace's date,
// never a correction of this one's (fillReleaseDate, #2518) - the guard still
// reads the date against it - and an older row of the first ASIN (a
// dump captured before the sync bot's last statement) would roll a fresher date
// back. A coarser date keeps the extend-only rule (fillReleaseDate).
// Measured in the dry run: 11 of the first 200 released-by-date recordings had
// been POSTPONED, libex listing a later date the catalogue never took, so they
// showed as released and every such row was refused as a contradiction.

// refreshFloorDivisor is the SANITY FLOOR of a refresh: a released runtime under
// a fifth of the recorded estimate is a broken value, not a correction, whatever
// chapter list agrees with it (a one-chapter placeholder agrees with itself).
// Measured on 2026-10-27 over the first 200 released preorder-era recordings by
// ASIN (libex-fill --released-preorders): the released runtime sat between 0.247
// and 2.52 times the estimate, every one with a chapter list agreeing with it
// within 10%; the low end is the Mr Gum titles, listed at 307-409 minutes and
// released at 78-123 (estimate.go's header has the same family at "75% below"),
// so a quarter would have refused real corrections and a fifth leaves margin.
// There is no ceiling: an estimate is as often far too short (rune-seeker-8, 800
// listed and 2,019 released) as too long, and the chapter agreement already
// refuses a stated value the timeline does not carry.
const refreshFloorDivisor = 5

// refreshRuntimeCredible is the sanity floor: recorded is the recording's
// runtime (0 when it records none, which no floor reads).
func refreshRuntimeCredible(recorded, stated int) bool {
	return stated > 0 && (recorded <= 0 || stated*refreshFloorDivisor >= recorded)
}

// thinnerChapters reports whether a row's chapter list of row entries is a
// DEGENERATE replacement for a recorded table of recorded entries, which a
// refresh never writes: a single chapter, or fewer than half as many, in place
// of a table of several. A listing that collapses its chapters (a one-chapter
// placeholder, a part-level table) agrees with its own runtime just as well, so
// only the count can tell, and a wrong table is worse than a stale one.
func thinnerChapters(row, recorded int) bool {
	return recorded > 1 && (row == 1 || row*2 < recorded)
}

// isFullDate reports whether s is a full YYYY-MM-DD date.
func isFullDate(s string) bool {
	return len(s) == len("2006-01-02") && datePattern.MatchString(s)
}

// awaitingRefresh is THE preorder-era test (see the header): bulk-mirror-only,
// first stated before its release date, and its FIRST ASIN (firstASIN, the one
// a refresh is read from) not yet stated on or after it.
func awaitingRefresh(release, addedAt, firstASIN string, sources []model.Source) bool {
	if firstASIN == "" || release == "" || len(release) > len("2006-01-02") || !datePattern.MatchString(release) || !model.BulkMirrorOnly(sources) {
		return false
	}
	// before reports whether a statement's day precedes the release at the
	// release's own precision (releasedAfter, the release validated above).
	before := func(stated string) bool {
		day := stated
		if len(day) > len(release) {
			// TimeKey normalizes a timestamp to its UTC day; a value it cannot
			// parse comes back as it is, and is compared as written.
			if k := model.TimeKey(day); len(k) >= len(release) {
				day = k[:len(release)]
			}
		}
		return stated != "" && release > day
	}
	first, firstKey := addedAt, ""
	for _, s := range sources {
		if s.ImportedAt == "" {
			continue
		}
		if k := model.TimeKey(s.ImportedAt); firstKey == "" || k < firstKey {
			first, firstKey = s.ImportedAt, k
		}
	}
	if !before(first) {
		return false
	}
	// The restatement: the first ASIN stated on or after the release - only a
	// refresh's stamp, which is read from that ASIN alone. A merged regional
	// ASIN's stamp is another ref and ends nothing.
	for _, s := range sources {
		if s.Ref == firstASIN && s.ImportedAt != "" && !before(s.ImportedAt) {
			return false
		}
	}
	return true
}

// rawAwaitingRefresh is awaitingRefresh over a recording held as decoded JSON.
func rawAwaitingRefresh(raw map[string]any) bool {
	return awaitingRefresh(coerceStr(raw["release_date"]), coerceStr(raw["added_at"]), rawFirstASIN(raw), rawSources(raw))
}

// rawFirstASIN is a decoded recording's FIRST ASIN (asin[0]), the one it was
// catalogued under and the only one a refresh or a moved date is read from;
// "" when it carries none. recordingFirstASIN is the same rule over a loaded
// recording.
func rawFirstASIN(raw map[string]any) string {
	arr, _ := raw["asin"].([]any)
	if len(arr) == 0 {
		return ""
	}
	first, _ := arr[0].(map[string]any)
	return NormalizeASIN(coerceStr(first["asin"]))
}

// recordingFirstASIN is rawFirstASIN over a loaded recording.
func recordingFirstASIN(r *model.Recording) string {
	if len(r.ASIN) == 0 {
		return ""
	}
	return NormalizeASIN(r.ASIN[0].ASIN)
}

// restates reports whether this run's stamp on raw is a refresh's RESTATEMENT,
// which stampSource writes past the source deduplication: the row refreshed the
// recording (curRefresh) and raw is that preorder-era recording - a work or a
// series stamped by the same row carries no release date and is never one.
func (p *planner) restates(raw map[string]any) bool {
	return p.curRefresh && rawAwaitingRefresh(raw)
}

// redates reports whether row b speaks for the preorder-era recording raw's
// listing (the header's FIRST ASIN paragraph) - the condition for a refresh and
// for a moved release date alike: a row of its FIRST ASIN that STATES the day it
// was captured (a libex row's updatedAt) and was captured no earlier than any
// statement the record holds of that ASIN. A row stating no capture day would be
// judged by the run's date, which says nothing about how fresh its listing is,
// so it replaces nothing.
func (p *planner) redates(raw map[string]any, b sourceBook) bool {
	asin := NormalizeASIN(b.str("asin"))
	if asin == "" || asin != rawFirstASIN(raw) || b.capturedAt == "" {
		return false
	}
	day := b.capturedAt
	for _, s := range rawSources(raw) {
		if k := model.TimeKey(s.ImportedAt); s.Ref == asin && len(k) >= len(day) && k[:len(day)] > day {
			return false
		}
	}
	return true
}

// judgeRefresh decides whether row b refreshes the preorder-era recording raw:
// a row of its FIRST ASIN (redate, p.redates), released, with a full date,
// stating a credible runtime, whose stamp ends the era - the run's day on or
// after the row's release date, the one the record carries once written.
// withheld (with the evidence that refuted the runtime) is a released candidate
// whose runtime the judgement refused - the caller notes it as a kept estimate
// once the ordinary path has let the row through.
func (p *planner) judgeRefresh(raw map[string]any, b sourceBook, redate bool) (refresh, withheld bool, against runtimeEvidence) {
	rd := b.str("release_date")
	if !redate || b.runtimeMin <= 0 || !isFullDate(rd) || !p.rowReleased(b) {
		return false, false, runtimeEvidence{}
	}
	if day := p.curSource.ImportedAt; len(day) != len("2006-01-02") || releasedAfter(rd, day) {
		return false, false, runtimeEvidence{}
	}
	ok, against := judgeOwnASINRuntime(raw, b)
	return ok, !ok, against
}

// judgeOwnASINRuntime is judgeMergedRuntime's twin for the recording's OWN
// ASIN, the same product, so no estimate bound reads the stated runtime as
// identity: it stands unless it is broken (refreshRuntimeCredible) or a chapter
// TIMELINE refutes it - the row's own list, else the recorded one, within 10%.
// A row list THINNER than the recorded table (thinnerChapters) is not the
// evidence: the refresh keeps the recorded table, so the runtime must agree
// with the timeline the record will carry, not with a placeholder that agrees
// with itself. The recorded runtime itself is never the evidence: one a merge
// already corrected is still not what the released listing is judged against.
func judgeOwnASINRuntime(raw map[string]any, b sourceBook) (ok bool, against runtimeEvidence) {
	stated := b.runtimeMin
	recordedChapters, _ := raw["chapters"].([]any)
	if rowChapters := rowChapterEvidence(b); len(rowChapters) > 0 && !thinnerChapters(len(rowChapters), len(recordedChapters)) {
		against = runtimeEvidence{min: chapterMinutes(rowChapters)}
	} else {
		tl, has := rawChapterMinutes(raw["chapters"])
		against = runtimeEvidence{min: tl, estimate: !has}
	}
	recorded, _ := coerceInt(raw["runtime_min"])
	if !refreshRuntimeCredible(int(recorded), stated) {
		return false, against
	}
	return against.estimate || runtimesCompatible(against.min, stated), against
}

// refreshFields is a preorder-era recording's four refreshable fields as JSON,
// read before and after the fill block so the counters say what really changed.
type refreshFields struct{ runtime, cover, chapters, release string }

func readRefreshFields(raw map[string]any) refreshFields {
	return refreshFields{jsonOf(raw["runtime_min"]), coerceStr(raw["cover_url"]), jsonOf(raw["chapters"]), coerceStr(raw["release_date"])}
}

// jsonOf is a decoded value's JSON - comparable whether it was decoded with
// UseNumber (a pack) or holds setDecoded's float64s.
func jsonOf(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}

// countRefresh counts what a refresh, or a moved preorder date, changed on the
// recording ref, from its fields before the fill block and raw after it.
func (p *planner) countRefresh(ref RecRef, before refreshFields, raw map[string]any, refreshed bool) {
	after := readRefreshFields(raw)
	if !refreshed {
		// A precision fill of a coarser recorded date (fillReleaseDate) is no move.
		if datesConflict(before.release, after.release) {
			p.summary.MovedPreorderDates++
			if len(p.refreshed.movedExamples) < maxWarnExamples {
				p.refreshed.movedExamples = append(p.refreshed.movedExamples,
					fmt.Sprintf("%s/%s %s -> %s", ref.Work, ref.Rec, before.release, after.release))
			}
		}
		return
	}
	p.summary.RefreshedRecordings++
	var fields []string
	for _, f := range []struct {
		name    string
		n       *int
		changed bool
	}{
		{"runtime", &p.summary.RefreshedRuntimes, after.runtime != before.runtime},
		{"cover", &p.summary.RefreshedCovers, after.cover != before.cover},
		{"chapters", &p.summary.RefreshedChapters, after.chapters != before.chapters},
		{"release date", &p.summary.RefreshedReleaseDates, after.release != before.release},
	} {
		if f.changed {
			*f.n++
			fields = append(fields, f.name)
		}
	}
	if len(fields) > 0 && len(p.refreshed.examples) < maxWarnExamples {
		p.refreshed.examples = append(p.refreshed.examples,
			fmt.Sprintf("%s/%s %s", ref.Work, ref.Rec, strings.Join(fields, ", ")))
	}
}

// reportRefreshes appends the run's aggregated notes about the refresh and the
// moved preorder dates.
func (p *planner) reportRefreshes() {
	s := &p.summary
	owner := func(n int) string {
		if n == 1 {
			return "its"
		}
		return "their"
	}
	if n := s.MovedPreorderDates; n > 0 {
		s.Notes = append(s.Notes, withExamples(fmt.Sprintf(
			"moved %d preorder release %s to the date a row of %s own ASIN states",
			n, plural(n, "date"), owner(n)), p.refreshed.movedExamples))
	}
	if n := s.RefreshedRecordings; n > 0 {
		s.Notes = append(s.Notes, withExamples(fmt.Sprintf(
			"refreshed %d released preorder %s from a row of %s own ASIN: %d %s, %d %s, %d %s, %d %s replaced",
			n, plural(n, "recording"), owner(n),
			s.RefreshedRuntimes, plural(s.RefreshedRuntimes, "runtime"), s.RefreshedCovers, plural(s.RefreshedCovers, "cover"),
			s.RefreshedChapters, plural(s.RefreshedChapters, "chapter list"), s.RefreshedReleaseDates, plural(s.RefreshedReleaseDates, "release date")),
			p.refreshed.examples))
	}
}

// SelectRefreshTargets returns the PREORDER-ERA recordings (awaitingRefresh)
// whose release date has passed by day (YYYY-MM-DD, at the release date's own
// precision): the recordings a released row would refresh, named by their first
// ASIN - one released row ends the state. Each asks for its chapter list when
// it has none, and for a released listing's in any case (RefreshChapters),
// since a release most often corrects or first supplies the table.
func SelectRefreshTargets(cat *model.Catalog, day string) []FillTarget {
	return selectTargets(cat, func(r *model.Recording) (FillTarget, bool) {
		ok := !releasedAfter(r.ReleaseDate, day) && awaitingRefresh(r.ReleaseDate, r.AddedAt, recordingFirstASIN(r), r.Sources)
		return FillTarget{NeedCover: r.CoverURL == "", NeedChapters: len(r.Chapters) == 0, RefreshChapters: true}, ok
	})
}
