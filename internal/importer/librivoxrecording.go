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
	lifeSeen := map[string]bool{}
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
func (p *planner) restateLibrivoxRecording(ref RecRef, f *librivoxFacts, lifeSeen map[string]bool) {
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
	set(rec, "librivox_genres", listOrNil(f.genres))
	set(rec, "credits", p.librivoxRecordingCredits(f))
	p.restateLibrivoxChapters(rec, f, set)
	if changed {
		p.putWorkEntry(ref.Work, entry)
		p.summary.LibrivoxRestated.Recordings++
	}
}

// librivoxRecordingCredits is the project's translators as the recording's
// credits, in LibriVox's order, each person once; nil when it states none or
// none resolves.
func (p *planner) librivoxRecordingCredits(f *librivoxFacts) any {
	var out []model.Credit
	seen := map[string]bool{}
	for _, c := range f.credits {
		if c.kind != librivoxAuthor || c.suffix != librivoxTranslatorSuffix {
			continue
		}
		slug, ok := p.librivoxTranslatorSlug(c, f.id)
		if !ok {
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
	return listOrNil(out)
}

// librivoxTranslatorSlug is the person a translator credit of project names:
// the one carrying its author id, else its name's person when that carries no
// other author id, else - the name resolving to no record at all - a person
// created there for them, carrying the id. ok=false otherwise.
func (p *planner) librivoxTranslatorSlug(c librivoxCredit, project int) (string, bool) {
	if c.id > 0 {
		if owner, ok := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; ok {
			return p.livePerson(owner)
		}
	}
	if slug, ok := p.librivoxCreditSlug(c); ok {
		if held := p.lv.authorOf[slug]; held != 0 && held != c.id {
			return "", false
		}
		return slug, true
	}
	name := p.soleCreditName(c)
	if name == "" || c.id <= 0 {
		return "", false
	}
	r := p.resolvePerson(name)
	if !r.create || r.fellBack {
		return "", false
	}
	p.curSource = OutSource{Type: p.sourceType, Ref: strconv.Itoa(project), ImportedAt: p.importDate}
	slug := p.createPerson(r.slug, r.name)
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
func (p *planner) restateLibrivoxChapters(rec map[string]any, f *librivoxFacts, set func(map[string]any, string, any)) {
	chs, _ := rec["chapters"].([]any)
	if len(chs) == 0 || len(chs) != len(f.sections) {
		if len(f.sections) > 0 {
			p.summary.LibrivoxRestated.Chapterless++
		}
		return
	}
	narrators := map[string]bool{}
	list, _ := rec["narrators"].([]any)
	for _, n := range list {
		if s, ok := n.(string); ok {
			narrators[s] = true
		}
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

// refreshLibrivoxLifeYears fills the stated life years of every author and
// translator of the row whose author id a person carries, once per person per
// run (an author credited on many projects states the same years on each).
func (p *planner) refreshLibrivoxLifeYears(f *librivoxFacts, seen map[string]bool) {
	for _, c := range f.credits {
		if c.kind != librivoxAuthor || c.id <= 0 || (c.life.birth == 0 && c.life.death == 0) {
			continue
		}
		owner, ok := p.lv.people[librivoxKey{librivoxAuthor, c.id}]
		if !ok {
			continue
		}
		slug, ok := p.livePerson(owner)
		if !ok || seen[slug] {
			continue
		}
		seen[slug] = true
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
