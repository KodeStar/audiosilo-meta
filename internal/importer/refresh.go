package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
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
// WHICH record: a PREORDER-ERA one, the estimate.go predicate
// (runtimeEstimated) - bulk-mirror-only, and every dated statement the mirror
// made about it predates its release date. A record any non-mirror source
// attested (a user library, an intake form, a hand edit citing one) is never
// refreshed, as it is never overwritten by the mirror at all.
//
// WHICH row: a bulk-mirror run's ENRICHMENT row (scopeFill) matched by ASIN, so
// it is about one of the recording's own ASINs by construction - the same
// product, which is why no estimate bound reads its runtime as identity. It must
// be RELEASED: its release date on or before the day it was captured
// (rowReleased), and its productState, where the row carries one, AVAILABLE -
// a libex record still AVAILABLE_FOR_PREORDER past its date has not been
// corrected yet. And it must state a CREDIBLE runtime (judgeReleasedRuntime with
// sameASIN): over the sanity floor, and agreeing within 10% with the row's own
// chapter list, else with the recorded one, where either exists. A row failing
// any of that is no refresh: it goes down the ordinary path, contradiction guard
// included, exactly as before this rule, and the record stays preorder-era for a
// later row.
//
// WHAT it replaces: runtime_min (the stated one), cover_url (the row's https
// cover), chapters (the row's list, which the judgement has just found to agree
// with the stated runtime - and libex-fill attaches only a payload passing
// acceptChapters) and release_date (the row's full date: a moved preorder date
// is not a contradiction for such a record). Narrators, abridged, publisher,
// asin[], isbn[] and the work stay as enrichment leaves them: publisher and
// ISBNs fill-absent, the rest untouched.
//
// HOW it ends: the refresh always writes, even when the row confirms every
// value, because its stamp is the post-release statement - dated the run's day,
// on or after the release, so endsEstimate lets it past the source
// deduplication and the record is no longer preorder-era. A later row is
// therefore ordinary enrichment again (fill-absent, fully guarded), and neither
// a re-run nor libex-fill --released-preorders selects the record a second time.

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

// releasedProductState is libex's productState for a product on sale.
const releasedProductState = "AVAILABLE"

// refreshRuntimeCredible is the sanity floor: recorded is the recording's
// runtime (0 when it records none, which no floor reads).
func refreshRuntimeCredible(recorded, stated int) bool {
	return stated > 0 && (recorded <= 0 || stated*refreshFloorDivisor >= recorded)
}

// refreshCandidate reports whether row b may refresh raw before its runtime is
// judged: a bulk-mirror enrichment row stating a runtime, released, about a
// preorder-era record.
func (p *planner) refreshCandidate(raw map[string]any, b sourceBook) bool {
	if !p.mirrorTier || b.runtimeMin <= 0 || !p.rowReleased(b) {
		return false
	}
	if b.productState != "" && b.productState != releasedProductState {
		return false
	}
	return rawRuntimeEstimated(raw)
}

// refreshRecording is the refresh of one record by one row, or nothing: it
// reports whether the row refreshed raw (and the caller then skips the
// contradiction guard - a moved date and a corrected runtime are what the
// refresh is for - and writes the record whatever changed). A candidate whose
// runtime the judgement withholds is left to the ordinary path, with withheld
// set to the note that reports it as a kept estimate, for the caller to call if
// that path does not refuse the row.
func (p *planner) refreshRecording(raw map[string]any, ref RecRef, b sourceBook) (refreshed bool, withheld func()) {
	if !p.refreshCandidate(raw, b) {
		return false, nil
	}
	recorded, _ := coerceInt(raw["runtime_min"])
	rowChapters, against, ok := judgeReleasedRuntime(raw, b, true)
	if !ok {
		return false, func() { p.noteKeptEstimate(ref, int(recorded), b.runtimeMin, against) }
	}
	var fields []string
	if int(recorded) != b.runtimeMin {
		if recorded > 0 {
			p.replaceEstimate(raw, ref, int(recorded), b.runtimeMin)
		} else {
			setDecoded(raw, "runtime_min", b.runtimeMin)
		}
		p.refreshed.runtimes++
		fields = append(fields, "runtime")
	}
	if img := b.str("image_url"); strings.HasPrefix(img, "https://") && coerceStr(raw["cover_url"]) != img {
		raw["cover_url"] = img
		p.refreshed.covers++
		fields = append(fields, "cover")
	}
	if len(rowChapters) > 0 && !sameDecoded(raw["chapters"], decodedForm("chapters", rowChapters)) {
		setDecoded(raw, "chapters", rowChapters)
		p.refreshed.chapters++
		fields = append(fields, "chapters")
	}
	// Only a FULL date replaces: a coarser one restates the recorded date at
	// another precision, and fillReleaseDate's extend-only rule handles it.
	if rd := b.str("release_date"); len(rd) == len("2006-01-02") && coerceStr(raw["release_date"]) != rd {
		raw["release_date"] = rd
		p.refreshed.releaseDates++
		fields = append(fields, "release date")
	}
	p.refreshed.recordings++
	if len(fields) > 0 && len(p.refreshed.examples) < maxWarnExamples {
		p.refreshed.examples = append(p.refreshed.examples,
			fmt.Sprintf("%s/%s %s", ref.Work, ref.Rec, strings.Join(fields, ", ")))
	}
	return true, nil
}

// sameDecoded reports whether two decoded JSON values say the same thing. They
// are compared as their JSON (a map's keys sorted), because a pack is decoded
// with UseNumber while setDecoded's form holds float64s.
func sameDecoded(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// reportRefreshes appends the run's one aggregated note about the refresh.
func (p *planner) reportRefreshes() {
	r := p.refreshed
	p.summary.RefreshedRecordings = r.recordings
	p.summary.RefreshedRuntimes = r.runtimes
	p.summary.RefreshedCovers = r.covers
	p.summary.RefreshedChapters = r.chapters
	p.summary.RefreshedReleaseDates = r.releaseDates
	if r.recordings == 0 {
		return
	}
	p.summary.Notes = append(p.summary.Notes, withExamples(fmt.Sprintf(
		"refreshed %d released preorder %s from a row of %s own ASIN: %d %s, %d %s, %d %s, %d %s replaced",
		r.recordings, plural(r.recordings, "recording"), pick(r.recordings, "its", "their"),
		r.runtimes, plural(r.runtimes, "runtime"), r.covers, plural(r.covers, "cover"),
		r.chapters, plural(r.chapters, "chapter list"), r.releaseDates, plural(r.releaseDates, "release date")),
		r.examples))
}

// pick is one or many by n.
func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// SelectRefreshTargets returns the PREORDER-ERA recordings (runtimeEstimated)
// whose release date has passed by day (YYYY-MM-DD, at the release date's own
// precision): the recordings a released row would refresh. Each is named by its
// first ASIN - one released row ends the state - and asks for its chapter list,
// since a preorder listing's table is what a release most often corrects or
// first supplies. Sorted by ASIN, as SelectFillTargets is, so --limit truncates
// deterministically.
func SelectRefreshTargets(cat *model.Catalog, day string) []FillTarget {
	var out []FillTarget
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			if len(r.ASIN) == 0 || r.ASIN[0].ASIN == "" || releasedAfter(r.ReleaseDate, day) ||
				!runtimeEstimated(r.ReleaseDate, r.AddedAt, r.Sources) {
				continue
			}
			out = append(out, FillTarget{
				ASIN: r.ASIN[0].ASIN, Work: w.ID, Recording: r.ID,
				NeedCover: r.CoverURL == "", NeedChapters: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ASIN < out[j].ASIN })
	return out
}
