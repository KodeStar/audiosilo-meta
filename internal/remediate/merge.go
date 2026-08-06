package remediate

import (
	"encoding/json"
	"fmt"

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
}

// compose builds the merged work for a group. A refusal returns ok false and
// leaves the group's parts exactly as they are.
func compose(idx *index, g *group, today string) (merged, Refusal, bool) {
	base, title, minted, r, ok := mergeBase(idx, g)
	if !ok {
		return merged{}, r, false
	}
	g.Slug, g.Title = base.slug, title

	work := base.work.clone()
	if err := work.set("id", base.slug); err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	if err := work.set("title", title); err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	mergeWorkFields(work, g)

	recKey, rec, err := mergeRecording(base, g, today)
	if err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	if err := work.setRecordings(map[string]obj{recKey: rec}); err != nil {
		return merged{}, internalRefusal(g, err), false
	}
	if g.Set != nil {
		work.set("sources", unionSources(work.sources(), //nolint:errcheck // marshalling a []model.Source cannot fail
			[]model.Source{{Type: model.SourceLibexImport, Ref: g.Set.ASIN, ImportedAt: today}}))
	}
	return merged{Slug: base.slug, Work: work, Minted: minted, Title: title, Parts: g.partSlugs()}, Refusal{}, true
}

// baseRecord is the record a merge starts from: the complete-set work already
// in the catalogue, or the group's first part.
type baseRecord struct {
	slug   string
	work   obj
	recKey string
	rec    obj
	// existing reports whether the base is a complete-set work rather than a
	// part. Its facts are the recorded ones, so they win.
	existing bool
}

// mergeBase decides the merged work's slug and title, and which record the
// merge composes on top of.
func mergeBase(idx *index, g *group) (baseRecord, string, bool, Refusal, bool) {
	if g.Target != "" {
		t := idx.candidates[g.Target]
		key, rec, ok := soleRecording(t.obj)
		if !ok { // already refused in matchTargets; unreachable, and loud if it ever is not
			return baseRecord{}, "", false, Refusal{
				Category: catMultiRecording, Subject: g.Base,
				Reason: "the complete-set work does not carry exactly one recording", Entries: []string{g.Target},
			}, false
		}
		// The existing work's title is a recorded product title; a dump row
		// cannot improve on it, and rewriting it would be churn.
		return baseRecord{slug: g.Target, work: t.obj, recKey: key, rec: rec, existing: true}, t.title(), false, Refusal{}, true
	}

	title := g.Base + dramatizedSuffix
	if g.Set != nil {
		title = g.Set.Title
	}
	slug := model.Slugify(title)
	if !model.ValidSlug(slug) {
		return baseRecord{}, "", false, Refusal{
			Category: catSlugCollision, Subject: g.Base,
			Reason:  fmt.Sprintf("the merged title %q does not slug to a valid id", title),
			Entries: g.partSlugs(),
		}, false
	}
	if idx.works[slug] {
		return baseRecord{}, "", false, Refusal{
			Category: catSlugCollision, Subject: g.Base,
			Reason:  fmt.Sprintf("the merged work would claim slug %q, which another work already holds", slug),
			Entries: append([]string{slug}, g.partSlugs()...),
		}, false
	}
	first := g.Parts[0]
	return baseRecord{slug: slug, work: first.Work, recKey: first.RecKey, rec: first.Rec}, title, true, Refusal{}, true
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

	setOrDrop(work, "genres", genres)
	setOrDropCredits(work, credits)
	work.set("sources", sources) //nolint:errcheck // marshalling a []model.Source cannot fail
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
			into.set("isbn", appendUnique(into.strs("isbn"), from.strs("isbn"))) //nolint:errcheck // a []string marshals
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
	work.set("xref", map[string]json.RawMessage(into)) //nolint:errcheck // raw members re-marshal
}

// mergeRecording composes the one recording that represents the production.
func mergeRecording(base baseRecord, g *group, today string) (string, obj, error) {
	rec := base.rec.clone()
	if err := rec.set("id", base.recKey); err != nil {
		return "", nil, err
	}
	if err := rec.set("work", g.Slug); err != nil {
		return "", nil, err
	}
	if !base.existing {
		// The base is PART ONE, so the facts that are about a part rather than
		// about the book have to be re-derived from the whole set: one part's
		// length is not the book's, and one part's abridged flag speaks only for
		// itself. Everything else a part states (its cover, its publisher, its
		// release date) is equally true of the book.
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
		if base.existing || p.Slug != g.Parts[0].Slug {
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
	case g.Complete && everyPartTimed && sumRuntime > 0:
		runtime, hasRuntime = sumRuntime, true
	}
	if hasRuntime {
		if err := rec.set("runtime_min", runtime); err != nil {
			return "", nil, err
		}
	} else {
		rec.drop("runtime_min")
	}

	if g.Set != nil {
		release = earlierDate(release, g.Set.releaseDay())
		// The whole-book product's own cover pictures the whole book; a part's
		// pictures the part. Only a RECORDED cover outranks it.
		if cover == "" {
			cover = g.Set.coverURL()
		}
		if publisher == "" {
			publisher = g.Set.Publisher
		}
		sources = unionSources(sources, []model.Source{{Type: model.SourceLibexImport, Ref: g.Set.ASIN, ImportedAt: today}})
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
	// none at all otherwise.
	if !base.existing || !rec.has("chapters") {
		rec.drop("chapters")
		if g.Set != nil {
			if chs := g.Set.chapterList(); len(chs) > 0 {
				if err := rec.set("chapters", chs); err != nil {
					return "", nil, err
				}
			}
		}
	}

	if !rec.has("abridged") {
		if v, ok := agreedAbridged(g); ok {
			if err := rec.set("abridged", v); err != nil {
				return "", nil, err
			}
		}
	}

	setStringsOrDrop(rec, "narrators", narrators)
	setASINsOrDrop(rec, asins)
	setISBNsOrDrop(rec, isbns)
	if err := rec.set("sources", sources); err != nil {
		return "", nil, err
	}
	setStringOrDrop(rec, "release_date", release)
	setStringOrDrop(rec, "added_at", added)
	setStringOrDrop(rec, "cover_url", cover)
	setStringOrDrop(rec, "publisher", publisher)
	return base.recKey, rec, nil
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

func setStringOrDrop(o obj, key, value string) {
	if value == "" {
		o.drop(key)
		return
	}
	o.set(key, value) //nolint:errcheck // a string marshals
}

func setStringsOrDrop(o obj, key string, values []string) {
	if len(values) == 0 {
		o.drop(key)
		return
	}
	o.set(key, values) //nolint:errcheck // a []string marshals
}

func setOrDrop(o obj, key string, values []string) { setStringsOrDrop(o, key, values) }

func setOrDropCredits(o obj, credits []model.Credit) {
	if len(credits) == 0 {
		o.drop("credits")
		return
	}
	o.set("credits", credits) //nolint:errcheck // a []model.Credit marshals
}

func setASINsOrDrop(o obj, asins []model.ASIN) {
	if len(asins) == 0 {
		o.drop("asin")
		return
	}
	o.set("asin", asins) //nolint:errcheck // a []model.ASIN marshals
}

func setISBNsOrDrop(o obj, isbns []model.ISBNRef) {
	if len(isbns) == 0 {
		o.drop("isbn")
		return
	}
	o.set("isbn", isbns) //nolint:errcheck // a []model.ISBNRef marshals
}

// internalRefusal wraps an encoding failure as a refusal, so one unencodable
// record stops that book rather than the run.
func internalRefusal(g *group, err error) Refusal {
	return Refusal{
		Category: catTwinDisagreement, Subject: g.Base,
		Reason: "the merged record could not be composed: " + err.Error(), Entries: g.partSlugs(),
	}
}
