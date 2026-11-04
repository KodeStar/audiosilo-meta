package importer

// librivoxrecording.go is the LibriVox RECORDING PASS: after planning and the
// people stamp, every LibriVox row of the run whose recording the catalogue
// holds - one it landed, or one it skipped as already held - has LibriVox's
// own statement about that project restated on the recording:
//
//   - librivox_title, the project's title verbatim ("Metamorphosis (version
//     2)"), and librivox_genres, its genre names verbatim, each once, in
//     LibriVox's order (LICENSING.md's public-domain exception);
//   - credits, the project's translators as `translator` credits on THE
//     RECORDING: a translation is a fact about the project, so two LibriVox
//     translations of one work credit their own translators, and a project
//     attached to a catalogued work no longer borrows that work's;
//   - on each chapter (when the stored chapters are the project's sections, one
//     to one), section_number where LibriVox's own number differs from the
//     1-based position (LibriVox numbers some projects from 0), and readers,
//     the section's readers as person slugs among the recording's narrators;
//   - the life years of every author and translator whose LibriVox author id a
//     person carries, filled where the record lacks them (refreshLifeYears).
//
// A reader is the person carrying their reader id (or this run's resolution of
// it, or their name's person when that holds no other reader id), and must be
// one of the recording's narrators. A translator is the person carrying their
// author id, or their name's person when that holds no other author id; one
// whose name resolves to NO record (a translator of a project that attached to a
// catalogued work, which records no work credit and so created nobody) is
// created there, carrying the author id and its years, as the planner would
// create them for a work of its own - the recording now credits them, so they
// are no orphan. Anything else it cannot resolve - a name held by another author
// id, a name that slugs to nothing - is left off and counted, so a credit is
// never guessed onto someone.
//
// Because a held project is restated too, a rerun of the import over a snapshot
// IS the backfill of these facts, and a second rerun changes nothing.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// librivoxSection is one LibriVox section, in chapter order: its own number (-1
// when the feed states none) and its readers, as indices into the row's
// librivoxFacts.credits.
type librivoxSection struct {
	number  int
	readers []int
}

// librivoxHeld is a row whose project the catalogue already held, and where.
type librivoxHeld struct {
	facts *librivoxFacts
	ref   RecRef
}

// LibrivoxRestated is what the recording pass did, for the run's report.
type LibrivoxRestated struct {
	// Recordings counts the LibriVox recordings whose stored record changed.
	Recordings int
	// Chapterless counts the recordings whose stored chapters are not the
	// project's sections one to one (none stored, or another count), so no
	// section number or reader was written on them.
	Chapterless int
	// StaleReadersDropped counts the chapter readers dropped from a recording
	// whose row lists no sections because they are no longer narrators (the
	// rest of its chapter facts are kept).
	StaleReadersDropped int
	// TranslatorCreditsDropped counts the stored recording credits a rerun
	// dropped: the row named its translators, every one resolved, and that
	// person was not among them.
	TranslatorCreditsDropped int
	// ReadersUnresolved counts the recordings left without chapter readers
	// because a section's reader resolved to none of its narrators.
	ReadersUnresolved int
	// TranslatorsCreated counts the translators created for a recording credit
	// (their name resolved to no record), and TranslatorsUnresolved the
	// translator credits left off the recording (another author id holds the
	// name, or it slugs to nothing).
	TranslatorsCreated    int
	TranslatorsUnresolved int
	// LifeYearsFilled counts the people a stated birth or death year was
	// written onto, and LifeYearConflicts those whose record states a year
	// LibriVox contradicts (kept, and warned).
	LifeYearsFilled   int
	LifeYearConflicts int
}

// restateLibrivoxRecordings is the pass, over the landed rows and then the held
// ones, each in row order.
func (p *planner) restateLibrivoxRecordings() {
	if p.fatal != nil || (len(p.lv.landed) == 0 && len(p.lv.heldRows) == 0) {
		return
	}
	lifeSeen := map[int]bool{}
	for _, l := range p.lv.landed {
		p.restateLibrivoxRecording(RecRef{Work: l.work, Rec: l.rec}, l.facts, lifeSeen)
	}
	for _, h := range p.lv.heldRows {
		p.restateLibrivoxRecording(h.ref, h.facts, lifeSeen)
	}
	if p.summary.LibrivoxRestated.TranslatorsCreated > 0 {
		// A translator created here exists now: the run's credits-not-recorded
		// report says so (it was settled before the pass ran).
		p.settleUnrecordedCredits()
	}
}

// restateLibrivoxRecording restates one project's facts on its recording.
func (p *planner) restateLibrivoxRecording(ref RecRef, f *librivoxFacts, lifeSeen map[int]bool) {
	if p.fatal != nil {
		return
	}
	p.refreshLibrivoxLifeYears(f, lifeSeen)
	ws := p.works[ref.Work]
	if ws == nil || ws.recs[ref.Rec] == nil {
		return // the recording is not where the run last saw it; nothing to restate
	}
	entry, rec := p.recordingRaw(ref.Work, ref.Rec)
	if rec == nil {
		return
	}
	changed := false
	set := func(m map[string]any, key string, v any) {
		if v == nil {
			if _, ok := m[key]; ok {
				delete(m, key)
				changed = true
			}
			return
		}
		if old, ok := m[key]; ok && sameJSON(old, v) {
			return
		}
		m[key] = v
		changed = true
	}
	if f.title != "" {
		set(rec, "librivox_title", f.title)
	}
	// A row stating no genres (a thinner fetch) says nothing about them: the
	// stored ones stay, as a section-less row's chapter facts do.
	if len(f.genres) > 0 {
		set(rec, "librivox_genres", f.genres)
	}
	if credits, write := p.librivoxRecordingCredits(f, rec["credits"]); write {
		set(rec, "credits", credits)
	}
	p.restateLibrivoxChapters(rec, f, set)
	if changed {
		p.putWorkEntry(ref.Work, entry)
		p.summary.LibrivoxRestated.Recordings++
	}
}

// librivoxRecordingCredits is the recording's credits as the row restates them
// over stored (the recording's current credits member), and whether to write
// them at all:
//
//   - a row stating no translator says nothing about them: the stored credits
//     stay (write false), as a section-less row's chapter facts do;
//   - otherwise its translators resolve (librivoxTranslatorSlug), in LibriVox's
//     order, each person once. A stored credit whose person is not among them
//     is dropped only when EVERY stated translator resolved - then the row has
//     said who translated it - and is counted
//     (LibrivoxRestated.TranslatorCreditsDropped). One that does not resolve
//     adds nothing, but it may be the very person stored, so then the stored
//     credits are kept after the resolved ones.
func (p *planner) librivoxRecordingCredits(f *librivoxFacts, stored any) (any, bool) {
	var out []model.Credit
	seen := map[string]bool{}
	stated, unresolved := 0, 0
	for _, c := range f.credits {
		if c.kind != librivoxAuthor || c.suffix != librivoxTranslatorSuffix {
			continue
		}
		stated++
		slug, ok := p.librivoxTranslatorSlug(c, f.id)
		if !ok {
			unresolved++
			p.summary.LibrivoxRestated.TranslatorsUnresolved++
			p.rowWarnings = append(p.rowWarnings, fmt.Sprintf(
				"librivox %d: translator %q (author id %d) resolves to no person; not credited on the recording", f.id, c.name, c.id))
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, model.Credit{Person: slug, Role: model.RoleTranslator})
	}
	if stated == 0 {
		return nil, false
	}
	list, _ := stored.([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		person, _ := m["person"].(string)
		role, _ := m["role"].(string)
		if person == "" || seen[person] {
			continue
		}
		if unresolved == 0 {
			p.summary.LibrivoxRestated.TranslatorCreditsDropped++
			continue
		}
		seen[person] = true
		out = append(out, model.Credit{Person: person, Role: role})
	}
	return listOrNil(out), true
}

// librivoxTranslatorSlug is the person a translator credit of project names,
// as the run decided it FOR ITS AUTHOR ID (resolveLibrivoxAuthors), never
// re-derived from the name:
//
//   - the person carrying the id (catalogued, or stamped by this run);
//   - else the credit's decided person (librivoxCredit.person: rule 1's
//     catalogued spelling, or rule 2's dated name for a same-name author who is
//     someone else) - created there, carrying the id and its years, when the
//     run has not made that record (an attach records no work credit, so
//     nothing else would);
//   - a credit with an id but no decided person (the person rules held it, or
//     its name slugs to nothing) is credited to nobody; one with NO id only to
//     a record its name already is, never created and never stamped. A held
//     row's translator ids are decided too (heldRowSkips).
//
// A record carrying ANOTHER LibriVox author id is never credited: the id, not
// the name, is the person.
func (p *planner) librivoxTranslatorSlug(c librivoxCredit, project int) (string, bool) {
	if c.id > 0 {
		if owner, ok := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; ok {
			return p.livePerson(owner)
		}
	}
	if c.id > 0 && c.person == nil {
		// Every author id the run meets is decided before planning, a held
		// row's translators included (heldRowSkips): no person
		// is the rules' hold, or a name that slugs to nothing. Neither is
		// credited - a hold is not a guess.
		return "", false
	}
	if c.person == nil {
		slug, ok := p.librivoxCreditSlug(c)
		if !ok || (p.lv.authorOf[slug] != 0 && p.lv.authorOf[slug] != c.id) {
			return "", false
		}
		return slug, true
	}
	r := p.resolveCredit(credit{person: c.person})
	if !r.create {
		if held := p.lv.authorOf[r.slug]; held != 0 && held != c.id {
			return "", false
		}
		return r.slug, true
	}
	if _, retired := p.redirects.Survivor(model.RedirectPeople, r.slug); retired {
		return "", false
	}
	// createPerson takes its provenance from the row being planned; the pass
	// runs after planning, so it names this project for the record and puts
	// the planner's own back.
	prev := p.curSource
	p.curSource = OutSource{Type: p.sourceType, Ref: strconv.Itoa(project), ImportedAt: p.importDate}
	slug := p.createPerson(r.slug, r.name)
	p.curSource = prev
	p.stampLibrivoxID(slug, c, project)
	p.summary.LibrivoxRestated.TranslatorsCreated++
	return slug, p.fatal == nil
}

// librivoxReaderOn is the narrator of a recording (narrators its slugs) that a
// reader credit names: the credit's person (librivoxCreditSlug: this run's
// resolution of the reader id, else the name's person) when that holds no other
// reader id, else the person carrying the id - the first of those that is one
// of the narrators.
func (p *planner) librivoxReaderOn(c librivoxCredit, narrators map[string]bool) (string, bool) {
	var cands []string
	if slug, ok := p.librivoxCreditSlug(c); ok {
		if held := p.lv.readerOf[slug]; held == 0 || held == c.id || c.id <= 0 {
			cands = append(cands, slug)
		}
	}
	if owner, ok := p.lv.people[librivoxKey{librivoxReader, c.id}]; ok && c.id > 0 {
		cands = append(cands, owner)
	}
	for _, s := range cands {
		if live, ok := p.livePerson(s); ok && narrators[live] {
			return live, true
		}
	}
	return "", false
}

// restateLibrivoxChapters writes each section's number and readers onto the
// stored chapter it is, when the stored chapters are the sections one to one
// (buildChapters keeps all of a project's sections or none). The readers are
// written on every chapter, absent where a section names none, unless the
// recording has ONE narrator who reads every section - then on none, which
// already says so (model.Chapter.Readers).
//
// Stored chapters that are NOT the sections one to one (another source's list,
// or an edit since) carry no section's number or readers: any this pass wrote
// earlier is cleared, so a reader who has since stopped being a narrator can
// never be left on a chapter (which pkg/check would refuse, failing the run).
// A row that lists NO sections at all says nothing about them (a thinner
// fetch), so it clears nothing it cannot contradict: the section numbers stay,
// and so does every chapter reader still one of the narrators
// (keepNarratingReaders).
func (p *planner) restateLibrivoxChapters(rec map[string]any, f *librivoxFacts, set func(map[string]any, string, any)) {
	chs, _ := rec["chapters"].([]any)
	narrators := map[string]bool{}
	list, _ := rec["narrators"].([]any)
	for _, n := range list {
		if s, ok := n.(string); ok {
			narrators[s] = true
		}
	}
	if len(f.sections) == 0 {
		p.keepNarratingReaders(chs, narrators, set)
		return
	}
	if len(chs) != len(f.sections) {
		p.summary.LibrivoxRestated.Chapterless++
		for _, v := range chs {
			if ch, ok := v.(map[string]any); ok {
				set(ch, "readers", nil)
				set(ch, "section_number", nil)
			}
		}
		return
	}
	readers := make([][]string, len(f.sections))
	resolved, solo := true, len(narrators) == 1
	for i, sec := range f.sections {
		for _, ci := range sec.readers {
			slug, ok := p.librivoxReaderOn(f.credits[ci], narrators)
			if !ok {
				resolved = false
				break
			}
			if !slices.Contains(readers[i], slug) {
				readers[i] = append(readers[i], slug)
			}
		}
		if !resolved {
			break
		}
		if len(readers[i]) != 1 {
			solo = false
		}
	}
	if !resolved {
		p.summary.LibrivoxRestated.ReadersUnresolved++
		p.rowWarnings = append(p.rowWarnings, fmt.Sprintf(
			"librivox %d: a section reader resolves to none of the recording's narrators; no chapter readers recorded", f.id))
	}
	for i, v := range chs {
		ch, ok := v.(map[string]any)
		if !ok {
			continue
		}
		var number any // an unstated number (-1) says nothing, as the position does
		if n := f.sections[i].number; n != i+1 && n >= 0 {
			number = n
		}
		set(ch, "section_number", number)
		var rd any
		if resolved && !solo {
			rd = listOrNil(readers[i])
		}
		set(ch, "readers", rd)
	}
}

// keepNarratingReaders is restateLibrivoxChapters for a row listing no
// sections: each chapter keeps the readers still among the narrators and drops
// the rest (counted, LibrivoxRestated.StaleReadersDropped). Should one narrator
// then be left listed on every chapter, the lists go entirely - that is the
// spelling pkg/check refuses, and absence already says it.
func (p *planner) keepNarratingReaders(chs []any, narrators map[string]bool, set func(map[string]any, string, any)) {
	kept := make([][]any, len(chs))
	every, dropped := len(chs) > 0, 0
	for i, v := range chs {
		ch, _ := v.(map[string]any)
		rs, _ := ch["readers"].([]any)
		for _, r := range rs {
			if s, ok := r.(string); ok && narrators[s] {
				kept[i] = append(kept[i], r)
			} else {
				dropped++
			}
		}
		every = every && len(kept[i]) > 0
	}
	if dropped == 0 {
		return
	}
	p.summary.LibrivoxRestated.StaleReadersDropped += dropped
	solo := every && len(narrators) == 1
	for i, v := range chs {
		ch, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if solo {
			set(ch, "readers", nil)
		} else {
			set(ch, "readers", listOrNil(kept[i]))
		}
	}
}

// refreshLibrivoxLifeYears fills the stated life years of every author and
// translator of the row whose author id a person carries, once per id per run:
// an id credited on many projects states the same years on each, but a person's
// two ids (model.Person.LibrivoxAuthorAliases) need not, and whichever row came
// first must not keep the other's years from being read.
func (p *planner) refreshLibrivoxLifeYears(f *librivoxFacts, seen map[int]bool) {
	for _, c := range f.credits {
		if c.kind != librivoxAuthor || c.id <= 0 || (c.life.birth == 0 && c.life.death == 0) {
			continue
		}
		owner, ok := p.lv.people[librivoxKey{librivoxAuthor, c.id}]
		if !ok {
			continue
		}
		slug, ok := p.livePerson(owner)
		if !ok || seen[c.id] {
			continue
		}
		seen[c.id] = true
		p.refreshLifeYears(slug, c, f.id)
		if p.fatal != nil {
			return
		}
	}
}

// refreshLifeYears writes a LibriVox author's stated birth and death years onto
// the person carrying their author id where the record lacks them. A year the
// record already states differently is never overwritten: the run warns naming
// both and writes nothing for that person (one of the two is wrong and nothing
// says which). A filled half must still pair with the other (death not before
// birth), else it is a conflict too.
func (p *planner) refreshLifeYears(slug string, c librivoxCredit, project int) {
	raw := p.entryRaw(pack.FamilyPeople, slug)
	if raw == nil {
		return
	}
	birth, hasBirth := rawYear(raw["birth_year"])
	death, hasDeath := rawYear(raw["death_year"])
	conflict := (hasBirth && c.life.birth != 0 && birth != c.life.birth) || (hasDeath && c.life.death != 0 && death != c.life.death)
	nb, nd := birth, death
	if !hasBirth {
		nb = c.life.birth
	}
	if !hasDeath {
		nd = c.life.death
	}
	if !conflict && nb != 0 && nd != 0 && nd < nb {
		conflict = true
	}
	if conflict {
		p.summary.LibrivoxRestated.LifeYearConflicts++
		p.rowWarnings = append(p.rowWarnings, fmt.Sprintf(
			"librivox %d: person %q states life years %s, LibriVox author %d (%q) states %s; kept",
			project, slug, yearsText(birth, death), c.id, c.name, yearsText(c.life.birth, c.life.death)))
		return
	}
	if nb == birth && nd == death {
		return
	}
	if nb != 0 {
		raw["birth_year"] = nb
	}
	if nd != 0 {
		raw["death_year"] = nd
	}
	srcArr, _ := raw["sources"].([]any)
	raw["sources"] = appendSourceUnique(srcArr, OutSource{Type: p.sourceType, Ref: strconv.Itoa(project), ImportedAt: p.importDate})
	p.putEntry(pack.FamilyPeople, slug, raw)
	p.lv.life[slug] = lifeYears{birth: nb, death: nd}
	p.summary.LibrivoxRestated.LifeYearsFilled++
}

// rawYear reads a stored year (a decoded number, or an int this run wrote),
// ok=false when the record states none.
func rawYear(v any) (int, bool) {
	if y, ok := v.(int); ok {
		return y, y != 0
	}
	n, ok := coerceInt(v)
	return int(n), ok && n != 0
}

// yearsText is a birth-death pair as the warnings print it ("1883-1924",
// "43 BC-?", "?-?"), each year as yearText spells it.
func yearsText(birth, death int) string {
	part := func(y int) string {
		if y == 0 {
			return "?"
		}
		return yearText(y)
	}
	return part(birth) + "-" + part(death)
}

// listOrNil is list as a field value: the list itself, or an untyped nil when
// it is empty, which the pass's setter reads as "drop the field".
func listOrNil[T any](list []T) any {
	if len(list) == 0 {
		return nil
	}
	return list
}

// sameJSON reports whether two decoded JSON values encode alike (a stored
// json.Number and an int this pass computed compare by their text).
func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
