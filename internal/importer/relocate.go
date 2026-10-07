package importer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Relocation records one moved production and the memberships that followed it.
// NewWork means its destination was minted by this run, including earlier rows.
type Relocation struct {
	Work                 string
	Recording            string
	Destination          string
	DestinationRecording string
	NewWork              bool
	Merged               bool
	Repointed            []string
}

type relocationPlan struct {
	evidence        map[string][]string
	unusable        map[string]bool
	candidates      []*model.Recording
	profile         *check.NarrationProfile
	seriesLanguages map[string]string
	newWorks        map[string]bool
	homeRecordings  map[string]bool
}

// All rows are evidence, even ones the ordinary libex parser refuses. Otherwise
// a bad-region copy could disappear before its contradictory language is read.
func relocationSetup(entries []rawBook, skips []RowSkip) func(*planner) {
	return func(p *planner) {
		p.existingSeriesOnly = true
		p.relocation = &relocationPlan{evidence: map[string][]string{}, unusable: map[string]bool{}, newWorks: map[string]bool{}}
		for _, e := range entries {
			asin := NormalizeASIN(e.str("asin"))
			lang, _ := mapLanguage(e.str("language"))
			p.relocation.evidence[asin] = append(p.relocation.evidence[asin], lang)
		}
		for _, s := range skips {
			p.relocation.unusable[s.ASIN] = true
		}
		p.summary.RelocationSkips = skips
		p.summary.Skips = nil
		p.summary.RelocationRefusals = map[string]int{}
	}
}

func (p *planner) prepareRelocation() {
	if p.loadProblems != 0 || p.catalog == nil {
		p.fatal = fmt.Errorf("relocation requires a valid catalogue; run metacheck first")
		return
	}
	r := p.relocation
	r.profile = check.NewNarrationProfile(p.catalog)
	r.seriesLanguages = map[string]string{}
	r.homeRecordings = map[string]bool{}
	for _, s := range p.catalog.Series {
		r.seriesLanguages[s.ID] = model.SeriesLanguage(s.Works, func(id string) string { return p.works[id].lang })
	}
	for _, w := range p.catalog.Works {
		for _, rec := range w.Recordings {
			if !model.SameLanguage(w.Language, rec.Language) {
				r.candidates = append(r.candidates, rec)
			} else {
				r.homeRecordings[w.ID] = true
			}
		}
	}
	// Candidate refusals are recorded once per input copy by this mode,
	// including copies that failed parsing. Keep ordinary parse codes only for
	// rows outside the relocation set.
	candidateASINs := map[string]bool{}
	for _, rec := range r.candidates {
		for _, a := range rec.ASIN {
			candidateASINs[a.ASIN] = true
		}
	}
	var otherSkips []RowSkip
	for _, skip := range p.summary.RelocationSkips {
		if !candidateASINs[skip.ASIN] {
			otherSkips = append(otherSkips, skip)
		}
	}
	p.summary.RelocationSkips = otherSkips
	sort.Slice(r.candidates, func(i, j int) bool {
		a, b := r.candidates[i], r.candidates[j]
		if a.Work != b.Work {
			return a.Work < b.Work
		}
		return a.ID < b.ID
	})
}

func (p *planner) planRelocate(books []sourceBook) {
	if p.fatal != nil {
		return
	}
	normalizeEditionMarkers(books)
	titles := resolveWorkTitles(books)
	suffixes := p.serialPositionSuffixes(books, titles)
	byASIN := map[string][]int{}
	for i, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		byASIN[asin] = append(byASIN[asin], i)
	}
	for _, rec := range p.relocation.candidates {
		indices, asins := []int{}, map[string]bool{}
		for _, a := range rec.ASIN {
			asins[a.ASIN] = true
		}
		seen := false
		for _, asin := range rawentry.SortedKeys(asins) {
			if len(p.relocation.evidence[asin]) > 0 {
				seen = true
			}
			indices = append(indices, byASIN[asin]...)
		}
		if !seen {
			continue
		}
		sort.Ints(indices)
		reason := p.relocationVeto(rec, asins)
		if reason.code == "" && len(indices) == 0 {
			reason = reasonRelocateRowUnusable
		}
		if reason.code != "" {
			p.refuseRelocation(rec, asins, reason)
			continue
		}
		p.relocateRecording(rec, asins, indices, books, titles, suffixes)
		if p.fatal != nil {
			return
		}
	}
}

func (p *planner) relocationVeto(rec *model.Recording, asins map[string]bool) refusal {
	lang := ""
	for _, asin := range rawentry.SortedKeys(asins) {
		for _, stated := range p.relocation.evidence[asin] {
			if stated == "" || (lang != "" && !model.SameLanguage(lang, stated)) {
				return reasonRelocateRowsLanguage
			}
			lang = stated
		}
	}
	if !model.SameLanguage(rec.Language, lang) {
		return reasonRelocateRecordingLanguage
	}
	ws := p.works[rec.Work]
	if ws.lang == "" || model.SameLanguage(ws.lang, lang) {
		return reasonRelocateWorkLanguage
	}
	// Own-language recordings never move in this mode, so the catalogue
	// snapshot remains valid throughout the run.
	if !p.relocation.homeRecordings[rec.Work] {
		return reasonRelocateNoHomeRecording
	}
	if p.relocation.profile.Of(rec.Narrators, rec.Work).Contradicts(lang) {
		return reasonRelocateNarration
	}
	for asin := range asins {
		if p.relocation.unusable[asin] {
			return reasonRelocateRowUnusable
		}
	}
	return refusal{}
}

func (p *planner) refuseRelocation(rec *model.Recording, asins map[string]bool, reason refusal, candidates ...string) {
	p.summary.RelocationRefusals[reason.code]++
	for _, asin := range rawentry.SortedKeys(asins) {
		for range p.relocation.evidence[asin] {
			p.summary.RelocationSkips = append(p.summary.RelocationSkips, RowSkip{ASIN: asin, Reason: reason.code, Candidates: candidates})
		}
	}
	if len(candidates) > 0 {
		reason.report += ": " + strings.Join(candidates, ", ")
	}
	p.summary.Warnings = append(p.summary.Warnings, fmt.Sprintf("%s: %s (%s)", recLabel(rec.Work, rec.ID), reason.report, reason.code))
}

// Resolve every usable row before creating anything: sibling ASINs must name
// one destination. Work creation itself remains the ordinary create path.
func (p *planner) relocateRecording(rec *model.Recording, asins map[string]bool, indices []int, books []sourceBook, titles, suffixes []string) {
	target := ""
	identityHomes := map[int]string{}
	for _, i := range indices {
		b := books[i]
		credits := p.rowAuthorCredits(b)
		if len(p.rowNarratorNames(b)) == 0 {
			p.refuseRelocation(rec, asins, reasonMissingNarrator)
			return
		}
		if len(credits) == 0 {
			p.refuseRelocation(rec, asins, reasonMissingAuthor)
			return
		}
		if titles[i] == "" {
			p.refuseRelocation(rec, asins, reasonMissingTitle)
			return
		}
		claim := p.rowSeriesClaim(b, titles[i], p.rowNarratorNames(b))
		lang, _ := mapLanguage(b.str("language"))
		authors := p.rowWorkAuthorsRO(credits)
		homes := p.relocationHomes(b, titles[i], suffixes[i], rec.Work, lang, authors)
		if len(homes) > 1 {
			p.refuseRelocation(rec, asins, reasonRelocateAmbiguousHome, homes...)
			return
		}
		slug := ""
		if len(homes) == 1 {
			slug = homes[0]
			identityHomes[i] = slug
		} else {
			walk := p.resolveWork(titles[i], b.str("title"), b.qualifiedTitle, suffixes[i], authors, lang, claim)
			slug = walk.free
			if walk.ws != nil {
				slug = walk.ws.slug
				if !model.SameLanguage(walk.ws.lang, lang) {
					slug = ""
				}
			}
		}
		if slug == "" || slug == rec.Work || (target != "" && target != slug) || p.repointConflict(b, rec.Work, slug, lang) {
			p.refuseRelocation(rec, asins, reasonRelocateDestination)
			return
		}
		target = slug
	}
	for _, i := range indices {
		b := books[i]
		p.setSource(NormalizeASIN(b.str("asin")))
		credits := p.rowAuthorCredits(b)
		authors := p.rowWorkAuthors(credits, p.bookWarn(b))
		lang, _ := mapLanguage(b.str("language"))
		claim := p.rowSeriesClaim(b, titles[i], p.rowNarratorNames(b))
		walk := workWalk{ws: p.works[identityHomes[i]]}
		if walk.ws == nil && p.relocation.newWorks[target] && p.works[target] != nil {
			// An earlier row of this recording minted the destination. Phase one
			// already agreed every row names it, so a sibling row whose credits
			// spell an author differently joins it rather than walking on to a
			// second candidate slug (which would abort the whole run below).
			walk = workWalk{ws: p.works[target]}
		}
		if walk.ws == nil {
			walk = p.resolveWork(titles[i], b.str("title"), b.qualifiedTitle, suffixes[i], authors, lang, claim)
		}
		if walk.ws == nil {
			p.relocation.newWorks[target] = true
		}
		facts := &workFacts{genres: b.genres, credits: credits}
		ws := p.getOrCreateWork(walk, authors, lang, facts, p.bookWarn(b))
		if ws == nil || ws.slug != target {
			p.fatal = fmt.Errorf("relocation destination changed while planning %s", recLabel(rec.Work, rec.ID))
			return
		}
		// A row joining a destination this run minted adds its credits and
		// genres (a no-op for the row that minted it, and for a catalogued work).
		p.mergeCreatedWorkFacts(ws, "", facts)
		p.rememberIdentity(p.rowIdentityOf(b, titles[i]), ws.slug, titles[i])
	}
	move := Relocation{Work: rec.Work, Recording: rec.ID, Destination: target, NewWork: p.relocation.newWorks[target]}
	p.moveRawRecording(rec, target, indices, books, &move)
	if p.fatal != nil {
		return
	}
	for _, i := range indices {
		b := books[i]
		for _, r := range b.series {
			if p.refusesToFound(r) {
				p.noteDroppedSeriesClaim(r)
				continue
			}
			if !r.seqOK {
				p.bookWarn(b)("series %q: missing or invalid position %q; not placed in series", r.name, r.rawSeq)
				continue
			}
			if p.repointMembership(r, rec.Work, target, rec.Language) {
				move.Repointed = append(move.Repointed, r.target.slug)
			}
			p.placeRelocatedClaim(r, target, titles[i], p.bookWarn(b))
		}
	}
	sort.Strings(move.Repointed)
	p.summary.Relocations = append(p.summary.Relocations, move)
	if move.Merged {
		p.summary.MergedIntoSibling++
	} else if move.NewWork {
		p.summary.RelocatedToNewWork++
	} else {
		p.summary.RelocatedToExisting++
	}
	p.summary.Notes = append(p.summary.Notes, fmt.Sprintf("relocated %s -> %s/%s (new work: %t, merged: %t, re-pointed: %v)", recLabel(rec.Work, rec.ID), target, move.DestinationRecording, move.NewWork, move.Merged, move.Repointed))
}

// relocationHomes asks the create guard's probe in the recording's language.
// A vetoed candidate is not a home; repeated hits name only one destination.
func (p *planner) relocationHomes(b sourceBook, title, suffix, old, lang string, authors workAuthors) []string {
	ident := p.rowIdentityOf(b, title)
	if ident.key == "" || suffix != "" || len(authors.identity) == 0 {
		return nil
	}
	homes := map[string]bool{}
	for _, match := range p.identityMatches(ident, title, lang, authors) {
		ws := p.works[match.work]
		if ws == nil || ws.slug == old || !model.SameLanguage(ws.lang, lang) {
			continue
		}
		homes[ws.slug] = true
	}
	return rawentry.SortedKeys(homes)
}

func (p *planner) relocationEntry(work string) rawentry.Obj {
	if p.fatal != nil {
		return nil
	}
	raw, ok, err := p.store.Get(pack.FamilyWorks, work)
	if err != nil {
		p.fatal = err
		return nil
	}
	if !ok {
		p.fatal = fmt.Errorf("relocation: missing work %q", work)
		return nil
	}
	obj, err := rawentry.Decode(raw)
	if err != nil {
		p.fatal = err
	}
	return obj
}

func (p *planner) moveRawRecording(rec *model.Recording, target string, indices []int, books []sourceBook, move *Relocation) {
	from, to := p.relocationEntry(rec.Work), p.relocationEntry(target)
	if p.fatal != nil {
		return
	}
	fromRecs, err := from.Recordings()
	if err != nil {
		p.fatal = err
		return
	}
	toRecs, err := to.Recordings()
	if err != nil {
		p.fatal = err
		return
	}
	raw := fromRecs[rec.ID]
	sources := raw.Sources()
	for _, i := range indices {
		sources = rawentry.UnionSources(sources, []model.Source{{Type: sourceLibex, Ref: NormalizeASIN(books[i].str("asin")), ImportedAt: p.importDate}})
	}
	raw.Set("sources", sources)
	// Every same-language recording of the destination is a merge candidate (repair
	// offers only the colliding key), and MoveRecording takes the FIRST same production
	// in key order - two qualifying siblings are one production recorded twice, and key
	// order keeps the choice deterministic.
	var candidates []string
	for _, key := range rawentry.SortedKeys(toRecs) {
		if model.SameLanguage(toRecs[key].Str("language"), rec.Language) {
			candidates = append(candidates, key)
		}
	}
	// ok is false only when freeKey gives up, and this freeKey never does.
	landedMove, _ := rawentry.MoveRecording(toRecs, raw, target, rec.ID, candidates, func() (string, bool) {
		for n := 1; ; n++ {
			key := NumberedSlugAt(rec.ID, n)
			if _, held := toRecs[key]; !held {
				return key, true
			}
		}
	})
	id := landedMove.ID
	move.Merged = landedMove.Merged
	if move.Merged {
		p.noteRelocationLosses(rec, target, id, raw, toRecs[id], landedMove.Lost)
	}
	delete(fromRecs, rec.ID)
	if err = from.SetRecordings(fromRecs); err != nil {
		p.fatal = err
		return
	}
	if err = to.SetRecordings(toRecs); err != nil {
		p.fatal = err
		return
	}
	p.putEntry(pack.FamilyWorks, rec.Work, from)
	p.putEntry(pack.FamilyWorks, target, to)
	delete(p.works[rec.Work].recs, rec.ID)
	landed := toRecs[id]
	runtime, _ := landed.IntAt("runtime_min")
	ri := &recInfo{narrators: ToSet(landed.Strs("narrators")), abridged: landed.BoolPtr("abridged"), asins: map[string]bool{},
		knownMin: knownMinutes(runtime, landed.Str("release_date"), landed.Str("added_at"), landed.Sources(),
			func() int { return chapterMinutes(landed.Chapters()) })}
	for _, a := range landed.ASINs() {
		ri.asins[a.ASIN] = true
	}
	p.works[target].recs[id] = ri
	move.DestinationRecording = id
}

func (p *planner) repointConflict(b sourceBook, old, target, lang string) bool {
	for _, r := range b.series {
		ss := p.seriesFor(r)
		if ss == nil || !r.seqOK || !model.SameLanguage(p.relocation.seriesLanguages[ss.slug], lang) {
			continue
		}
		if pos, ok := ss.members[old]; ok && SameSlot(pos, r.seq) {
			if dest, held := ss.members[target]; held && !SameSlot(dest, pos) {
				return true
			}
		}
	}
	return false
}

func (p *planner) repointMembership(r seriesRef, old, target, lang string) bool {
	ss := p.seriesFor(r)
	if ss == nil || !model.SameLanguage(p.relocation.seriesLanguages[ss.slug], lang) {
		return false
	}
	pos, held := ss.members[old]
	if !held || !SameSlot(pos, r.seq) {
		return false
	}
	p.loadSeriesRaw(ss)
	if p.fatal != nil {
		return false
	}
	works, _ := ss.raw["works"].([]any)
	_, already := ss.members[target]
	out := make([]any, 0, len(works))
	for _, item := range works {
		member, _ := item.(map[string]any)
		if member["work"] == old {
			if already {
				continue
			}
			member["work"] = target
		}
		out = append(out, item)
	}
	ss.raw["works"] = out
	delete(ss.members, old)
	delete(ss.positions, pos)
	if !already {
		ss.members[target] = pos
		ss.positions[pos] = target
	}
	ss.dirty = true
	p.summary.MembershipsRepointed++
	return true
}

// Sorting the source objects before parsing makes titles, credits, notes and
// source stamps independent of the order of duplicate and regional rows.
func sortedRelocationRows(entries []rawBook) {
	type keyedRow struct {
		row rawBook
		key string
	}
	rows := make([]keyedRow, len(entries))
	for i, row := range entries {
		key, _ := json.Marshal(row) // decoded JSON values are always marshalable
		rows[i] = keyedRow{row: row, key: string(key)}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	for i, row := range rows {
		entries[i] = row.row
	}
}

// Keep relocation's raw-value note format, including fields repair deliberately
// leaves unstated (added_at). When the longer mover timeline wins, report the
// sibling's discarded timeline instead.
func (p *planner) noteRelocationLosses(rec *model.Recording, target, id string, mover, landed rawentry.Obj, losses []rawentry.RecordingLoss) {
	lostRaw := map[string]json.RawMessage{}
	for _, loss := range losses {
		lostRaw[loss.Field] = loss.DroppedRaw
	}
	for _, field := range rawentry.SortedKeys(mover) {
		switch field {
		case "id", "work", "asin", "isbn", "sources", "narrators":
			// narrators: a same-production merge requires the same SET, so an
			// order difference is not a discarded fact.
			continue
		}
		dropped := mover[field]
		if value, lost := lostRaw[field]; lost {
			dropped = value
		}
		if !reflect.DeepEqual(rawentry.DecodeOr[any](dropped), rawentry.DecodeOr[any](landed[field])) {
			p.summary.Notes = append(p.summary.Notes, fmt.Sprintf("%s merged into %s/%s: discarded %s=%s", recLabel(rec.Work, rec.ID), target, id, field, dropped))
		}
	}
}

// SameSlot also sees numeric equivalents such as 1 and 1.0. Leave a held
// membership verbatim and never place beside an equivalent occupied slot.
func (p *planner) placeRelocatedClaim(r seriesRef, target, title string, warn func(string, ...any)) {
	ss := p.seriesFor(r)
	if ss == nil {
		return
	}
	pos := p.placementPosition(r, target, title, warn)
	if held, ok := ss.members[target]; ok && SameSlot(held, pos) {
		return
	}
	for _, other := range rawentry.SortedKeys(ss.members) {
		if other != target && SameSlot(ss.members[other], pos) {
			warn("series %q position %q already taken by %q; %q not added", r.name, pos, other, target)
			return
		}
	}
	p.addToSeries(r, target, pos, warn)
}
