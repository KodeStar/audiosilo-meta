package remediate

import (
	"encoding/json"
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// merge.go composes one book's merged work out of its parts, and out of the
// complete-set work the catalogue already holds when there is one.
//
// Every value it writes traces to a part record or to a supplied dump row.
// Where neither states a fact the field is omitted, never guessed - which is
// why runtime_min disappears from an incomplete set rather than being summed
// from the parts that happen to be here.

// dramatizedSuffix is the edition marker a derived title carries. It is the
// catalogue's dominant spelling: of the whole-book GraphicAudio dramatizations
// already recorded, the square-bracket form outnumbers the parenthesized one,
// and the part titles themselves are written that way.
const dramatizedSuffix = " [Dramatized Adaptation]"

// merged is a composed work, ready to be written.
type merged struct {
	Slug string
	Work obj
	// Minted reports whether the work is new, as against an existing
	// complete-set work this run enriched.
	Minted bool
	// Title is the merged work's title, for the report.
	Title string
	// Parts names the part works it absorbs, in part order.
	Parts []string
	// Runtime is the merged recording's runtime in minutes, 0 when the merge
	// deliberately states none.
	Runtime int
}

// libexSource is the provenance a complete-set dump row contributes. One
// spelling, used by both the work and its recording.
func libexSource(asin, today string) []model.Source {
	return []model.Source{{Type: model.SourceLibexImport, Ref: asin, ImportedAt: today}}
}

// compose builds the merged work for a group. A refusal returns ok false and
// leaves the group's parts exactly as they are. It reads the group and writes
// nothing back to it: the merged identity travels out in the result.
func compose(idx *index, g *group, today string) (merged, Refusal, bool) {
	base, r, ok := mergeBase(idx, g)
	if !ok {
		return merged{}, r, false
	}

	work := base.work.clone()
	work.set("id", base.slug)
	work.set("title", base.title)
	mergeWorkFields(work, g)

	rec, err := mergeRecording(base, g, today)
	if err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	if err := work.setRecordings(map[string]obj{base.recKey: rec}); err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	if g.Set != nil {
		work.set("sources", unionSources(work.sources(), libexSource(g.Set.ASIN, today)))
	}
	runtime, _ := rec.intAt("runtime_min")
	return merged{
		Slug: base.slug, Work: work, Minted: base.minted, Title: base.title,
		Parts: g.partSlugs(), Runtime: runtime,
	}, Refusal{}, true
}

// baseRecord is the record a merge starts from - the complete-set work already
// in the catalogue, or the group's first part - plus the identity the merged
// work takes.
type baseRecord struct {
	slug   string
	title  string
	work   obj
	recKey string
	rec    obj
	// minted reports that the merged work is new. Its complement, "the base is
	// an existing complete-set work whose recorded facts win", is the same bit:
	// a merge either has a target or it mints one.
	minted bool
}

// mergeBase decides the merged work's slug and title, and which record the
// merge composes on top of.
func mergeBase(idx *index, g *group) (baseRecord, Refusal, bool) {
	if g.Target != "" {
		t := idx.candidates[g.Target]
		key, rec, ok := t.soleRecording()
		if !ok { // already refused in matchTargets; unreachable, and loud if it ever is not
			return baseRecord{}, Refusal{
				Category: catMultiRecording, Subject: g.Base,
				Reason: "the complete-set work does not carry exactly one recording", Entries: []string{g.Target},
			}, false
		}
		// The existing work's title is a recorded product title; a dump row
		// cannot improve on it, and rewriting it would be churn.
		return baseRecord{slug: g.Target, title: t.title(), work: t.obj, recKey: key, rec: rec}, Refusal{}, true
	}

	title := g.Base + dramatizedSuffix
	if g.Set != nil {
		title = g.Set.Title
	}
	slug := mintedSlug(title)
	if !model.ValidSlug(slug) {
		return baseRecord{}, Refusal{
			Category: catSlugCollision, Subject: g.Base,
			Reason:  fmt.Sprintf("the merged title %q does not slug to a valid id", title),
			Entries: g.partSlugs(),
		}, false
	}
	if idx.works[slug] {
		return baseRecord{}, Refusal{
			Category: catSlugCollision, Subject: g.Base,
			Reason:  fmt.Sprintf("the merged work would claim slug %q, which another work already holds", slug),
			Entries: append([]string{slug}, g.partSlugs()...),
		}, false
	}
	first := g.Parts[0]
	return baseRecord{slug: slug, title: title, work: first.Work, recKey: first.RecKey, rec: first.Rec, minted: true}, Refusal{}, true
}

// mintedSlug is the merged work's id: the title's slug, with the EDITION MARKER
// held back as a disambiguating tail so it always survives.
//
// A plain Slugify would hard-cut a long title at MaxSlugLen, and what it cuts
// off is the "-dramatized-adaptation" end - the very thing that keeps the
// dramatization off the plain edition's slug ("Elantris: Tenth Anniversary
// Author's Definitive Edition" is 51 bytes short of the cap before the marker
// is appended). BoundedSlugTail is the project's answer to exactly that shape:
// shorten the BASE at a word boundary, keep the tail whole.
func mintedSlug(title string) string {
	marker := dramatizedMarker.FindString(title)
	base := model.Slugify(dramatizedMarker.ReplaceAllString(title, ""))
	if base == "" || marker == "" {
		return base
	}
	return importer.BoundedSlugTail(base, "-"+model.Slugify(marker))
}

// mergeWorkFields folds the parts' work-level facts into the merged work.
func mergeWorkFields(work obj, g *group) {
	genres := work.strs("genres")
	credits := work.credits()
	sources := work.sources()
	added := work.str("added_at")
	language := work.str("language")

	for _, p := range g.Parts {
		genres = unionGenres(genres, p.Work.strs("genres"))
		credits = unionCredits(credits, p.Work.credits())
		sources = unionSources(sources, p.Work.sources())
		added = earlierStamp(added, p.Work.str("added_at"))
		if language == "" {
			language = p.Work.str("language")
		}
		fillXref(work, p.Work)
	}

	setListOrDrop(work, "genres", genres)
	setListOrDrop(work, "credits", credits)
	work.set("sources", sources)
	setStringOrDrop(work, "added_at", added)
	setStringOrDrop(work, "language", language)
}

// fillXref fills the merged work's cross-references from a part's, member by
// member and only where the merged work states nothing. A recorded value is
// never replaced: two parts of one book that disagree about a QID are a fact
// somebody has to look at, not one to overwrite.
func fillXref(work, part obj) {
	src, ok := part["xref"]
	if !ok {
		return
	}
	from, err := decodeObj(src)
	if err != nil {
		return
	}
	into := obj{}
	if cur, ok := work["xref"]; ok {
		if into, err = decodeObj(cur); err != nil {
			return
		}
	}
	for _, k := range sortedKeys(from) {
		if k == "isbn" {
			into.set("isbn", appendUnique(into.strs("isbn"), from.strs("isbn")))
			continue
		}
		if !into.has(k) {
			into.setRaw(k, from[k])
		}
	}
	if len(into) == 0 {
		work.drop("xref")
		return
	}
	work.set("xref", map[string]json.RawMessage(into))
}

// mergeRecording composes the one recording that represents the production.
func mergeRecording(base baseRecord, g *group, today string) (obj, error) {
	rec := base.rec.clone()
	rec.set("id", base.recKey)
	rec.set("work", base.slug)
	if base.minted {
		// The base is PART ONE, so the facts that are about a part rather than
		// about the book have to be re-derived from the whole set: one part's
		// length is not the book's, one part's abridged flag speaks only for
		// itself, and one part's cover pictures the part.
		rec.drop("runtime_min")
		rec.drop("abridged")
		rec.drop("cover_url")
		rec.drop("publisher")
	}

	narrators := rec.strs("narrators")
	asins := rec.asins()
	isbns := rec.isbns()
	sources := rec.sources()
	release := rec.str("release_date")
	added := rec.str("added_at")
	cover := rec.str("cover_url")
	publisher := rec.str("publisher")
	var publishers []string
	runtime, hasRuntime := rec.intAt("runtime_min")

	// A complete-set row's identifier goes FIRST: it names the whole-book
	// product, which is what the merged recording now is.
	if g.Set != nil {
		asins = unionASINs([]model.ASIN{{Region: g.Set.Region, ASIN: g.Set.ASIN}}, asins)
	}

	sumRuntime, everyPartTimed := 0, true
	for _, p := range g.Parts {
		if !base.minted || p.Slug != g.Parts[0].Slug {
			narrators = appendUnique(narrators, p.Rec.strs("narrators"))
			asins = unionASINs(asins, p.Rec.asins())
			isbns = unionISBNs(isbns, p.Rec.isbns())
			sources = unionSources(sources, p.Rec.sources())
		}
		release = earlierDate(release, p.Rec.str("release_date"))
		added = earlierStamp(added, p.Rec.str("added_at"))
		publishers = append(publishers, p.Rec.str("publisher"))
		if n, ok := p.Rec.intAt("runtime_min"); ok {
			sumRuntime += n
		} else {
			everyPartTimed = false
		}
	}

	// Runtime, in the order of what the fact is worth: the whole-book product's
	// own stated length, then the recorded one, then the sum of a COMPLETE part
	// set, then nothing. Summing an incomplete set would state a length no
	// source states.
	switch {
	case g.Set != nil && g.Set.LengthMinutes > 0:
		runtime, hasRuntime = g.Set.LengthMinutes, true
	case hasRuntime:
	case g.Complete() && everyPartTimed && sumRuntime > 0:
		runtime, hasRuntime = sumRuntime, true
	}
	if hasRuntime {
		rec.set("runtime_min", runtime)
	} else {
		rec.drop("runtime_min")
	}

	if g.Set != nil {
		release = earlierDate(release, g.Set.ReleaseDate)
		// The whole-book product's own cover pictures the whole book; a part's
		// pictures the part. Only a RECORDED cover outranks it.
		if cover == "" {
			cover = g.Set.CoverURL
		}
		if publisher == "" {
			publisher = g.Set.Publisher
		}
		sources = unionSources(sources, libexSource(g.Set.ASIN, today))
	}
	if cover == "" {
		cover = partCover(g)
	}
	if publisher == "" {
		// The imprint the parts agree on, ties broken by the value, so a set
		// recorded under two spellings of one house has one answer.
		publisher = modal(publishers)
	}

	// Chapters are a TIMELINE. Two parts' timelines cannot be concatenated into
	// the whole book's without inventing offsets, so the merged recording keeps
	// the whole-book chapters when a whole-book source states them and carries
	// none at all otherwise. The dump row's list has already been through the
	// importer's structural rules, so an unusable one arrives here as nil.
	if base.minted || !rec.has("chapters") {
		rec.drop("chapters")
		if g.Set != nil {
			setListOrDrop(rec, "chapters", g.Set.Chapters)
		}
	}

	if !rec.has("abridged") {
		if v, ok := agreedAbridged(g); ok {
			rec.set("abridged", v)
		}
	}

	setListOrDrop(rec, "narrators", narrators)
	setListOrDrop(rec, "asin", asins)
	setListOrDrop(rec, "isbn", isbns)
	rec.set("sources", sources)
	setStringOrDrop(rec, "release_date", release)
	setStringOrDrop(rec, "added_at", added)
	setStringOrDrop(rec, "cover_url", cover)
	setStringOrDrop(rec, "publisher", publisher)
	return rec, nil
}

// partCover is the first cover any part states, in part order.
func partCover(g *group) string {
	for _, p := range g.Parts {
		if c := p.Rec.str("cover_url"); c != "" {
			return c
		}
	}
	return ""
}

// agreedAbridged returns the abridged flag every part states, if they all state
// it and all state the same. Absence means unknown, so a set that is silent
// anywhere stays silent.
func agreedAbridged(g *group) (bool, bool) {
	var value bool
	for i, p := range g.Parts {
		raw, ok := p.Rec["abridged"]
		if !ok {
			return false, false
		}
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return false, false
		}
		if i == 0 {
			value = v
			continue
		}
		if v != value {
			return false, false
		}
	}
	return value, len(g.Parts) > 0
}

// internalRefusal reports a record this package could not compose. It is a bug
// in this package rather than a problem with the data, and it has its own
// category so it can never be read as one of the data judgements beside it.
func internalRefusal(g *group, err error) Refusal {
	return Refusal{
		Category: catInternal, Subject: g.Base,
		Reason: "the merged record could not be composed: " + err.Error(), Entries: g.partSlugs(),
	}
}
