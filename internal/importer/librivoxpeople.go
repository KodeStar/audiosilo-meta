package importer

// librivoxpeople.go is the LibriVox import's four PERSON rules (meta#2778),
// the classes LibriVox tranches 7 to 11 fixed by hand in every tranche, and the
// one decision they feed (meta#2799): the person every author or translator
// credit names is decided ONCE for the batch, keyed by its LibriVox author id,
// before any row is planned and after the batch's credit decisions (the
// censuses and the initials step) - resolveLibrivoxAuthors - and carried on
// the credit (librivoxCredit.person, credit.person). Every later reader (the
// work's identity and credits, the narrators' rule 3, the claims, the stamp,
// the report) asks resolveCredit for that person instead of resolving the
// name again, so a rule judges the person the import credits, and nothing the
// cleaning does to a name afterwards (the honorific fold) can move a credit
// the id decided. Decisions are taken in the order of the lowest project
// crediting each id, so a batch decides the same whatever order its rows
// arrive in.
//
//  1. THE FOLD. The honorific fold (honorific.go) strips a courtesy title when
//     the bare name is a credit too. On a LibriVox row the ids say more than
//     the census does, so:
//     a. a credit whose id the catalogue holds is its holder, spelled as the
//     holder is: the id, not the spelling, is the person, so "Mrs. C. M.
//     Livingston" (1456) stays on `mrs-c-m-livingston`;
//     b. a titled credit is not folded onto a bare twin that is another
//     LibriVox author id of the batch, whatever their years (the owner's
//     rule: two ids are never folded into one person by a run - "Mrs. William
//     Platt" 14096 beside "William Platt" 13717 are two people, Livingston
//     1456 and 15071 LibriVox's own duplicate, and only a person can tell
//     them apart; the pair is listed, Summary.LibrivoxAuthorPairs), nor onto
//     a catalogued bare record that is another person by rule 2's test ("Sir
//     Hugh Fraser" is not `hugh-fraser`, the actor who narrates Poirot), and
//     when that test is unsure the row goes to the worklist as rule 2's does;
//     a titled name the fold leaves meets rule 2 under its own spelling;
//     c. a bare credit about to mint a new record resolves onto the ONE
//     catalogued twin carrying a courtesy title or a military rank
//     (librivoxTitledTwin): "Frederick Treves" onto `sir-frederick-treves`,
//     "Joseph H. Alexander" onto `colonel-joseph-h-alexander`, and LibriVox's
//     undated duplicate record "C. M. Livingston" (15071) onto a catalogued
//     `mrs-c-m-livingston`. Not when the twin only narrates, nor when the
//     life years disagree, nor when the twin carries another author id and
//     the credit states years of its own.
//     d. a pair a reviewed merge-people rejection names (Options.Distinct) is
//     two people in both directions: 1b reads it as kept apart, judged by every
//     slug the two spellings can land on, and 1c does not resolve onto that twin.
//  2. SAME-NAME AUTHORS. An author id no person carries, whose name resolves to
//     a catalogued person who is someone else, is not credited (and stamped)
//     there. Someone else means: the person carries ANOTHER librivox_author_id
//     and the two do not state the same birth and death years (John Brown
//     1715-1766 is not `john-brown`, 1830-1922; Margaret Steele Anderson under
//     two ids with one pair of years is one person); or the person states
//     years that disagree (a flourished year outside their life included); or,
//     carrying neither id nor years, they are credited only as a narrator (but
//     a LibriVox reader is the undated id's own volunteer), or only on works
//     first published more than librivoxLateMargin years after the author's
//     death, flourished year, or - stating a birth year alone - the end of a
//     librivoxLifespan from it, with no title in common with the id's
//     projects (librivoxCreditEvidence; a generic title such as "Poems" or
//     "Selected Essays" is no title in common, librivoxGenericTitle, though
//     "Poems (Ballads of a Cheechako)" is).
//     A work's first publication is its TEXT's date - its first_published,
//     else the copyright year a LibriVox recording of it states - else the
//     earliest release date of its recordings, and a work co-credited with
//     someone who died within the margin is an old text whatever its
//     recordings say; a work with no date at all is no evidence against the
//     match. A release date dates a modern book and a modern edition of an old
//     one alike, so works dated only by releases leave the id unsure - unless
//     a project the id authors, alone or with one other author, shows they
//     are editions (librivoxReleasesAreEditions:
//     the catalogue holds that very text under its full title, dated only by a
//     release past the bound, credited to a catalogued co-author of the
//     project (the same person, by slug, not merely a surname) - the
//     Sojourner Truth hold of #2798; a translator's project shows nothing of
//     the kind, so Margaret Hunt stays a worklist row and Thomas Taylor, the
//     translator of Proclus, never lands on the modern novelist). Instead the id is minted at its DATED name
//     (librivoxDatedName: "James Grant (1822-1887)" -> `james-grant-1822-1887`,
//     "Richard Wilson (fl. 1916)" -> `richard-wilson-fl-1916`), the form the
//     tranches minted by hand; when LibriVox states no year it is rule 4's;
//     when the dates are unsure its rows go to the worklist as
//     `same-name-author` (J. J. Howard fl. 1807 against a modern J.J. Howard),
//     never stamped silently. Two ids of the batch meeting one person are
//     decided as a later run would decide them: the first keeps it, a second
//     is the same person only on the same birth and death years, else dated,
//     or rule 4's when it states no year.
//  3. READERS ON AUTHORS. A reader whose name meets a catalogued person holding
//     no reader id claims them, unless that person is an AUTHOR record
//     (librivoxAuthorRecord: a catalogued librivox_author_id, or a life that
//     ended before LibriVox began, librivoxFounded, lifeEnd - its own, or that
//     of an author id of the run finally landing on it, settleLibrivoxPeople,
//     decided before any row is planned):
//     the reader is then a person of their own at `<name>-librivox-<reader
//     id>`, as for a second reader of one name (`adam-smith-librivox-19341`,
//     not Adam Smith 1723-1790 narrating a 2023 recording). A living or
//     undated author id of the same run is not an author record to a reader:
//     the two are one person, whichever row the run meets first. In
//     librivoxReaderSlug.
//
//  4. AUTHOR-ID FORMS. An author id with no year that rule 2 keeps off a
//     same-name person is a person of its own at
//     model.LibrivoxAuthorSlug (`ralph-scott-librivox-author-16428`, Ralph
//     Scott beside a retail narrator; Andy Adams 12675 beside `andy-adams`,
//     940), and an author or translator whose name slugs to nothing at
//     model.LibrivoxUnslugAuthorSlug (`librivox-author-17538`, Зинаида
//     Гиппиус), the record keeping the name as LibriVox spells it and the
//     stamp writing the id that makes the slug legal (mintLibrivoxAuthor) -
//     the reader forms' twins. A split beside a same-name person is listed
//     (Summary.LibrivoxAuthorSplits): LibriVox may list one person under two
//     ids, and only a person can tell. Never off a collective statement
//     ("Anonymous" under another id stays a same-name-author row), and never
//     onto a slot another person or a tombstone holds: the id then falls back
//     to the worklist (a sluggable name) or to a credit not recorded (one
//     that slugs to nothing). A later run finds the record by its id.
//
// A project the catalogue already holds is never planned (it is skipped before
// planning; its readers are librivoxReaderSlug's), and decides nothing here but
// its TRANSLATORS' ids, after every planned id (heldRowSkips): the recording
// pass restates them on its recording. So a rerun of a landed tranche changes
// nothing once those are credited.

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// librivoxFounded is the year LibriVox began (August 2005): a person who died
// before it read nothing for it.
const librivoxFounded = 2005

// librivoxLifespan bounds a life LibriVox states by its birth year alone: no
// one born in 1840 read for LibriVox (2005) or wrote a 2020 first edition. A
// flourished year alone bounds it the same way, since the person was born
// before it: no one who flourished in 1807 read for LibriVox either.
const librivoxLifespan = 100

// lifeEnd is the last year a life can have reached: its death year, else its
// birth year, else its flourished year, plus librivoxLifespan; 0 when it
// states none.
func lifeEnd(l lifeYears) int {
	switch {
	case l.death != 0:
		return l.death
	case l.birth != 0:
		return l.birth + librivoxLifespan
	case l.fl != 0:
		return l.fl + librivoxLifespan
	}
	return 0
}

// librivoxLateMargin is how many years after an author's death a work may
// first appear and still be evidence for them (posthumous editions and
// collections). Every hand-fixed tranche 9-11 case is decades past it: a 2014
// novel against a 1910 death, Mash against 1600.
const librivoxLateMargin = 25

// librivoxRankPrefixes are the military ranks rule 1c reads as a title in
// front of a name, beside honorificPrefixes. The general fold never strips
// them (honorific.go: a rank is often the name of record), so they only ever
// lead a bare LibriVox credit TO a catalogued titled record, never away.
var librivoxRankPrefixes = []string{
	"admiral", "adm", "brigadier", "captain", "capt", "colonel", "col", "commander", "cmdr",
	"general", "gen", "lieutenant", "lt", "major", "maj", "sergeant", "sgt",
}

// deTitled is deHonorified with the military ranks too.
func deTitled(name string) (bare string, stripped bool) {
	return dePrefixed(name, honorificPrefixes, librivoxRankPrefixes)
}

// lvPersonWork is one catalogued work crediting a person, with the year it was
// first published as far as the catalogue says (0 = unknown).
type lvPersonWork struct {
	slug, title string
	year        int
	// published says year dates the work's TEXT - its stated first_published,
	// or the copyright year a LibriVox recording of it states - not the
	// earliest release of its recordings.
	published bool
	// people is every person the work credits.
	people []string
	// died is the earliest death year any person the work credits states (0 =
	// none): a work co-credited with someone long dead is an old text, however
	// recent its recordings (a translator of Grimm).
	died int
}

// librivoxTitleKeys is every key rule 2 reads a title in common by: the
// spelling-variant key (librivoxTitleKey) of the title and of each form with
// its trailing qualifiers removed one at a time ("Metamorphosis (1915)" is
// "Metamorphosis"), and the duplicate-identity key (titlerule.IdentityTitleKey:
// "Metamorphosis: A Novella"). A shared title is the strongest sign that a
// catalogued author is the LibriVox one, whatever their works' dates say,
// except a generic one (librivoxGenericWordsOnly), which keys nothing: two
// poets may each publish "Poems". The test is per form, so a generic base
// with a qualifier that names a work ("Poems (Ballads of a Cheechako)") keeps
// its full title's key while its bare "Poems" keys nothing.
//
// The keys fold a volume total away ("Vol. 1 of 3" and "Vol. 1 of 5" share
// one) and, unlike the work lookup (librivoxTitleWorks), nothing puts it
// back, on purpose: the question here is WHO wrote the text, not WHICH
// division of it a work is. Two volume sets of one text are one author's,
// so a shared title says the same about the author whatever the totals; the
// work lookup refuses them because merging two divisions would be a wrong
// work, which this rule never makes.
func librivoxTitleKeys(title string) map[string]bool {
	keys := map[string]bool{}
	for t := title; t != ""; {
		if k := librivoxTitleKey(t); k != "" && !librivoxGenericWordsOnly(t) {
			keys[k] = true
		}
		stripped := stripTrailingQualifier(t)
		if stripped == t {
			break
		}
		t = stripped
	}
	// The identity key sets trailing qualifiers aside itself, so a generic
	// base under a qualifier would key as the bare base: none.
	if k := titlerule.IdentityTitleKey(title, ""); k != "" && !librivoxGenericTitle(title) {
		keys["identity:"+k] = true
	}
	return keys
}

// librivoxWorkKeys is librivoxTitleKeys of a catalogued work's title, memoized
// for the run's decisions (librivoxCreditEvidence asks it for every work of
// every person it judges).
func (p *planner) librivoxWorkKeys(w lvPersonWork) map[string]bool {
	if keys, ok := p.lv.workKeys[w.slug]; ok {
		return keys
	}
	if p.lv.workKeys == nil {
		p.lv.workKeys = map[string]map[string]bool{}
	}
	keys := librivoxTitleKeys(w.title)
	p.lv.workKeys[w.slug] = keys
	return keys
}

// librivoxGenericWords are the words a generic collection title is made of:
// what kind of writing it holds ("Poems", "Letters", "Short Stories"), how it
// was gathered ("Selected", "Collected", "Complete") and the glue between
// them ("Essays and Other Pieces", "The Poems of"). Deliberately small: a
// title with any other word in it names something.
var librivoxGenericWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and of other some more few various
		selected select selections collected complete miscellaneous minor early later last new
		short shorter poems poem poetry verse verses rhymes sonnets songs ballads hymns lyrics
		essays essay letters letter stories story tales tale fables sketches pieces works
		writings prose plays sermons speeches addresses lectures papers collection anthology
		volume vol book part`) {
		m[w] = true
	}
	return m
}()

// librivoxGenericTitle reports whether a title, its trailing qualifiers
// removed ("Poems (1915)"), is made only of librivoxGenericWords and numbers,
// at least one a word (a title of numbers alone, "1601", names a work): a
// title many authors share, so sharing it says nothing about who wrote it.
func librivoxGenericTitle(title string) bool {
	return librivoxGenericWordsOnly(stripTrailingQualifiers(title))
}

// librivoxGenericWordsOnly is librivoxGenericTitle's test of title as it
// stands, qualifiers and all: "Poems (1915)" is generic, "Poems (Ballads of a
// Cheechako)" is not.
func librivoxGenericWordsOnly(title string) bool {
	generic := false
	for _, w := range strings.Split(model.Slugify(title), "-") {
		switch {
		case strings.TrimLeft(w, "0123456789") == "":
			continue
		case !librivoxGenericWords[w]:
			return false
		}
		generic = true
	}
	return generic
}

// lvAuthorClaim is one LibriVox author id no person carries, as the batch's
// rows to be planned state it (or, for an id only a held row's translator
// credit names, the held rows: heldRowSkips): its cleaned name (CleanCreditName) and the slug
// of it ("" on the catch-all), the spellings the import's census cleans its
// first credit to with the honorific fold (folded: the name it is credited by
// unless a rule decides otherwise) and without it (unfolded), its life years
// and the titles of its projects, keyed on first use (titleKeys). The first
// credit is the one on the lowest project id, so a claim never depends on the
// order the rows arrive in.
type lvAuthorClaim struct {
	id, project      int
	name, slug       string
	folded, unfolded string
	life             lifeYears
	titles           []string
	projects         []lvClaimProject
	keys             map[string]bool
}

// titleKeys is the union of librivoxTitleKeys over the claim's project
// titles, built on first use: most claims are decided without them.
func (cl *lvAuthorClaim) titleKeys() map[string]bool {
	if cl.keys == nil {
		cl.keys = map[string]bool{}
		for _, t := range cl.titles {
			maps.Copy(cl.keys, librivoxTitleKeys(t))
		}
	}
	return cl.keys
}

// lvHold is one author id rule 2 sent to the worklist: the person its name
// meets (against: catalogued, or the person an earlier id of the run lands
// on) and that person's catalogued works, the worklist row's candidates.
// deferred says against is a person of this run, whose works are named once
// the run has planned them (finishLibrivoxHolds), as a later run would name
// them.
type lvHold struct {
	against    string
	candidates []LibrivoxCandidate
	deferred   bool
}

// The LibriVox rows a per-id decision walks (librivoxRows): those to be planned,
// those whose project the catalogue already holds, or both.
type lvRowSet int

const (
	lvPlannedRows lvRowSet = iota
	lvHeldRows
	lvAllRows
)

// librivoxRows is the indexes of the LibriVox rows of set, ordered by project
// id: the order every per-id decision is taken in, whatever order the rows
// arrive in.
func (p *planner) librivoxRows(books []sourceBook, set lvRowSet) []int {
	var rows []int
	for i := range books {
		b := &books[i]
		if b.lv == nil {
			continue
		}
		if held := p.lv.ids[b.lv.id]; set == lvAllRows || held == (set == lvHeldRows) {
			rows = append(rows, i)
		}
	}
	slices.SortStableFunc(rows, func(a, b int) int { return books[a].lv.id - books[b].lv.id })
	return rows
}

// resolveLibrivoxAuthors decides, once for the batch and before any row is
// planned, the person every LibriVox author and translator credit names, keyed
// by its author id, and puts it on the credit (librivoxCredit.person, which
// rowAuthorCreditsIn carries onto the row's credits): every later reader - the
// work's identity and credits, the stamp, the claims, the report - reads that
// person rather than resolving the name again. A catalogued id is its holder;
// an id no person carries is decided by rules 1 and 2 (decideLibrivoxAuthor),
// in the order of the lowest project crediting each, AFTER the batch's credit
// decisions (the censuses and the initials step, creditContextOf), so a rule
// judges the very person the import would credit. A credit with no id, or one
// whose name slugs to nothing, keeps its name's resolution.
func (p *planner) resolveLibrivoxAuthors(books []sourceBook) {
	// The indexes below are read only here.
	defer func() { p.lv.works, p.lv.workDates, p.lv.workKeys, p.lv.titledTwins = nil, nil, nil, nil }()
	quiet, unfolded := p.credits.author.quiet(), p.credits.author.unfolded()
	claims := map[int]*lvAuthorClaim{}
	bySlug := map[string][]*lvAuthorClaim{}
	holder := map[string]lifeYears{}
	var decided []string
	decide := func(order []*lvAuthorClaim) {
		for _, cl := range order {
			decided = append(decided, p.decideLibrivoxAuthor(cl, bySlug, holder)...)
		}
	}
	decide(p.collectLibrivoxClaims(books, lvPlannedRows, claims, bySlug, quiet, unfolded, nil))
	// Then the translators of the HELD rows, under the same rules but only
	// once every planned row's ids are decided, so they cannot move a planned
	// row's decision: see heldRowSkips.
	decide(p.collectLibrivoxClaims(books, lvHeldRows, claims, bySlug, quiet, unfolded, heldRowSkips))
	for i := range books {
		if b := &books[i]; b.lv != nil {
			for j, c := range b.lv.credits {
				if c.kind == librivoxAuthor && c.id > 0 {
					p.setLibrivoxPerson(b, j, p.lv.authorPins[c.id])
				}
			}
		}
	}
	// After the pins are on the credits, so a held row's worklist entry spells
	// its authors as a planned row's does (librivoxAuthorList).
	p.worklistHeldLibrivoxTranslators(books)
	if len(decided) > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"%d LibriVox author id(s) kept off a same-name or courtesy-title twin, or credited by another name (the rest the person rules held are on the worklist as same-name-author): %s",
			len(decided), strings.Join(decided, "; ")))
	}
}

// collectLibrivoxClaims gathers, over the rows of set in project order, one
// claim per author id no person carries - its name, folded and unfolded
// spellings, years, titles and per-project evidence - returning the claims it
// opened in the order their lowest project met them. A catalogued id pins its
// holder instead. A credit skip refuses (nil: none) is passed over, and so is
// one whose id an EARLIER call opened a claim for: that claim is already
// decided, by its own rows' evidence, so a later set cannot move it.
func (p *planner) collectLibrivoxClaims(books []sourceBook, set lvRowSet, claims map[int]*lvAuthorClaim,
	bySlug map[string][]*lvAuthorClaim, quiet, unfolded creditCensus, skip func(librivoxCredit) bool) []*lvAuthorClaim {
	var order []*lvAuthorClaim
	mine := map[int]bool{}
	for _, i := range p.librivoxRows(books, set) {
		b := &books[i]
		var people []string // librivoxCreditPeople, resolved once per row
		for _, c := range b.lv.credits {
			if c.kind != librivoxAuthor || c.id <= 0 || c.index >= len(b.authors) || (skip != nil && skip(c)) {
				continue
			}
			if owner, held := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; held {
				if live, ok := p.livePerson(owner); ok && p.lv.authorPins[c.id] == nil {
					p.lv.authorPins[c.id] = pinAt(live, p.people[live])
				}
				continue
			}
			cl := claims[c.id]
			if cl == nil {
				cl = &lvAuthorClaim{id: c.id, project: b.lv.id, name: CleanCreditName(c.name), life: c.life}
				cl.folded, _ = creditWithRolesSided(b.authors[c.index], quiet)
				cl.unfolded, _ = creditWithRolesSided(b.authors[c.index], unfolded)
				if slug, fellBack := personSlug(cl.name); !fellBack {
					cl.slug = slug // librivoxauthors.go rule 2: never on the catch-all
					bySlug[slug] = append(bySlug[slug], cl)
				}
				claims[c.id] = cl
				mine[c.id] = true
				order = append(order, cl)
			} else if !mine[c.id] {
				continue
			}
			// A project the id only TRANSLATES is no title evidence of who
			// they are (librivoxCreditEvidence's title in common): a
			// translation is another author's book, so a same-name person's
			// own book of that title - a novelist's 2015 "Metamorphosis"
			// beside Kafka's - says nothing about its translator
			// (librivoxReleasesAreEditions reads a translation the same way).
			// The translator is the one role LibriVox states besides the
			// author, so this is every project the id does not author; a role
			// added later keeps the evidence until it is reasoned about.
			if c.suffix != librivoxTranslatorSuffix {
				cl.titles = append(cl.titles, b.str("title"))
			}
			if people == nil {
				people = p.librivoxCreditPeople(b, quiet)
			}
			cl.projects = append(cl.projects, librivoxClaimProject(b, c, people))
		}
	}
	return order
}

// heldRowSkips is the credits a held row leaves out of the per-id decisions:
// every one but a TRANSLATOR. A held row is never planned, but the recording pass
// (librivoxrecording.go) restates its project's translators on its recording.
// A translator of a project that attached to a catalogued work recorded no work
// credit, so no person carries the id, and without a decision the pass would
// credit nobody. Such an id is judged by the planned ids' own rules 1 and 2
// (decideLibrivoxAuthor: the same years and id checks, the same-name hold, the
// author-id forms) over the held rows' evidence, after every planned id is
// decided - bySlug and holder carry those, so a held id meeting a planned id's
// name is judged against it, never the other way. An id the rules hold stays
// without a person, so the pass credits nobody (a hold is not a guess), and the
// project goes on the same-name-author worklist
// (worklistHeldLibrivoxTranslators). Its years reach settleLibrivoxPeople, so a
// pre-LibriVox translator's person is an author record a same-name reader is
// kept off. A held row's authors decide nothing: the work already credits them.
func heldRowSkips(c librivoxCredit) bool { return c.suffix != librivoxTranslatorSuffix }

// worklistHeldLibrivoxTranslators puts each held project one of whose
// translator ids is left without a person on the same-name-author worklist the
// planned rows use, naming the works of the person the name meets: its
// recording stays without that translator until a person settles who they
// are, and a rerun then credits them. Once per project, whichever rows decided
// the id - a held row's own, or a planned row's (whose project is then on the
// worklist too). An id is left without a person when the rules held it, or
// when they found it the same person as a record that already carries ANOTHER
// LibriVox author id (H. P. Paull 6787 beside 7024, the same years): two
// author ids never fold automatically, so the recording pass refuses that
// record (librivoxTranslatorSlug), and the row is for a person to settle.
func (p *planner) worklistHeldLibrivoxTranslators(books []sourceBook) {
	for _, i := range p.librivoxRows(books, lvHeldRows) {
		b := books[i]
		for _, c := range b.lv.credits {
			if c.kind != librivoxAuthor || c.id <= 0 || heldRowSkips(c) {
				continue
			}
			if h, ok := p.heldTranslatorLeft(c.id); ok {
				if h.deferred {
					p.lv.pendingHolds = append(p.lv.pendingHolds, lvPending{entry: len(p.summary.LibrivoxWorklist), against: h.against})
				}
				p.noteLibrivoxWorklist(b, b.str("title"), LibrivoxSameNameAuthor, h.candidates)
				break
			}
		}
	}
}

// heldTranslatorLeft is the hold a held row's translator id is left without a
// person by (worklistHeldLibrivoxTranslators): the rules' own, or one naming
// the catalogued record carrying another author id the id was decided onto.
// ok=false for an id the recording pass credits.
func (p *planner) heldTranslatorLeft(id int) (lvHold, bool) {
	if h, held := p.lv.sameName[id]; held {
		return h, true
	}
	if pin := p.lv.authorPins[id]; pin != nil {
		if live, ok := p.livePerson(pin.slug); ok && p.lv.authorOf[live] != 0 && p.lv.authorOf[live] != id {
			return lvHold{against: live, candidates: p.librivoxPersonCandidates(live)}, true
		}
	}
	return lvHold{}, false
}

// settleLibrivoxPeople takes the run's person decisions that read where the
// author ids finally land, before any row is planned and after
// keyLibrivoxAuthorVariants (planCreate), which can still move an id onto a
// catalogued spelling of it: the persons rule 3 keeps readers off (runAuthors:
// an author id whose years end before LibriVox began makes its person an
// author record, whichever row the run meets first), every reader id's person
// (decideLibrivoxReaders), the honorific folds the import makes
// (noteLibrivoxFolds) and the author id pairs rule 1b kept apart
// (Summary.LibrivoxAuthorPairs). A living or undated author is no author
// record to a reader: a LibriVox author or translator may well read too (rule
// 2's reader exception), so a reader of that name is that person in either
// order.
func (p *planner) settleLibrivoxPeople(books []sourceBook) {
	// Each author id's stated years, first seen: the planned rows' credits, then
	// the held rows' translators (the ids resolveLibrivoxAuthors decides).
	life := map[int]lifeYears{}
	for _, set := range []lvRowSet{lvPlannedRows, lvHeldRows} {
		for _, i := range p.librivoxRows(books, set) {
			for _, c := range books[i].lv.credits {
				if set == lvHeldRows && heldRowSkips(c) {
					continue
				}
				if _, seen := life[c.id]; c.kind == librivoxAuthor && c.id > 0 && !seen {
					life[c.id] = c.life
				}
			}
		}
	}
	for id, pin := range p.lv.authorPins {
		if end := lifeEnd(life[id]); pin != nil && end != 0 && end < librivoxFounded {
			p.lv.runAuthors[pin.slug] = true
		}
	}
	for _, pr := range p.lv.pairs {
		p.summary.LibrivoxAuthorPairs = append(p.summary.LibrivoxAuthorPairs,
			LibrivoxAuthorPair{Titled: p.librivoxPairAuthor(pr[0]), Bare: p.librivoxPairAuthor(pr[1])})
	}
	p.decideLibrivoxReaders(books)
	p.noteLibrivoxFolds(books)
}

// LibrivoxAuthorPair is two LibriVox author ids of one run that the honorific
// fold would make one person - a courtesy title on one ("Mrs. C. M.
// Livingston", 1456), the bare name on the other ("C. M. Livingston", 15071) -
// kept as two because LibriVox lists them under two ids (librivoxpeople.go
// rule 1b). Only a person can tell LibriVox's own duplicate record (Livingston)
// from two people (Mrs. William Platt and William Platt): merging is theirs to
// do, by moving the projects and the ids onto one record.
type LibrivoxAuthorPair struct {
	Titled, Bare LibrivoxPairAuthor
}

// LibrivoxPairAuthor is one side of a LibrivoxAuthorPair: the author id, its
// name as LibriVox states it, the person the run credits it to ("" when the
// person rules held its rows) and the lowest project crediting it.
type LibrivoxPairAuthor struct {
	AuthorID int    `json:"librivox_author_id"`
	Name     string `json:"name"`
	Person   string `json:"person"`
	Project  int    `json:"librivox_id"`
}

// String is the pair as the import report prints it, one line:
//
//	1456 "Mrs. C. M. Livingston" -> mrs-c-m-livingston (project 4714); 15071 "C. M. Livingston" -> c-m-livingston (project 9999)
func (pr LibrivoxAuthorPair) String() string {
	side := func(a LibrivoxPairAuthor) string {
		person := a.Person
		if person == "" {
			person = "held"
		}
		return fmt.Sprintf("%d %q -> %s (project %d)", a.AuthorID, a.Name, person, a.Project)
	}
	return side(pr.Titled) + "; " + side(pr.Bare)
}

// librivoxPairAuthor is one claim of a LibrivoxAuthorPair, as decided.
func (p *planner) librivoxPairAuthor(cl *lvAuthorClaim) LibrivoxPairAuthor {
	a := LibrivoxPairAuthor{AuthorID: cl.id, Name: cl.name, Project: cl.project}
	if pin := p.lv.authorPins[cl.id]; pin != nil {
		a.Person = pin.slug
	}
	return a
}

// decideLibrivoxAuthor applies rules 1 and 2 to one claim and records its
// person (pinLibrivoxAuthor) or its hold (holdLibrivoxAuthor), returning the
// run notes it makes. holder is the life years each person a claim of the run
// already lands on will carry, which a second id meeting that person is
// judged against.
func (p *planner) decideLibrivoxAuthor(cl *lvAuthorClaim, bySlug map[string][]*lvAuthorClaim, holder map[string]lifeYears) []string {
	id, name := cl.id, cl.name
	if cl.slug == "" {
		// A name that slugs to nothing is filed by its author id (rule 4),
		// never on the shared catch-all; when that slot is not free the
		// credit is dropped as unsluggable, as before.
		if slug, ok := model.LibrivoxUnslugAuthorSlug(name, id); ok && !p.mintLibrivoxAuthor(cl, slug, "") {
			// The slot is another person's or a tombstone's: the credit is
			// not recorded, and says why (librivoxSlotTaken).
			p.lv.slotTaken[id] = slug
			p.summary.Warnings = append(p.summary.Warnings, fmt.Sprintf(
				"librivox author %d (%q) slugs to nothing and %q is another person's id or a retired one; its credits are not recorded (%s)",
				id, name, slug, librivoxSlotTaken))
			p.summary.RunLevelWarnings++
		}
		return nil
	}
	var notes []string
	credited := cl.folded
	if bare, titled := deHonorified(name); titled {
		against, v := p.librivoxFoldVerdict(bare, cl, bySlug)
		switch {
		case v == lvUnsure:
			p.holdLibrivoxAuthor(id, against, false)
			return nil
		case v == lvApart:
			credited = cl.unfolded
			notes = append(notes, fmt.Sprintf("%d %q kept apart from %q", id, name, bare))
		case against != "":
			// Credited as the census folds it: onto the catalogued bare
			// person the verdict accepted.
			p.pinLibrivoxAuthor(cl, cl.folded)
			return nil
		}
		// The titled name itself still meets rule 2 below.
	}
	to, against, v := p.librivoxAuthorName(credited, cl)
	if r := p.resolvePerson(credited); v == lvSame && to == "" && !r.fellBack {
		// A second id of this run on a person another id lands on is judged
		// as librivoxVerdict judges a person already carrying an id, by the
		// years that person will carry: a catalogued person's own, else the
		// first id's (stampLifeYears).
		if life, ok := holder[r.slug]; ok && !sameLife(life, cl.life) {
			v, against = lvApart, r.slug
		} else if !ok {
			life, dated := p.lv.life[r.slug]
			if !dated {
				life = cl.life
			}
			holder[r.slug] = life
		}
	}
	switch v {
	case lvApart:
		if to = p.librivoxDatedName(id, name, cl.life); to == "" {
			// No year to mint a dated name by: a person of their own at
			// the author-id form (rule 4), held when that slot is not free
			// or the name is a collective statement's.
			if slug, ok := model.LibrivoxAuthorSlug(name, id); ok && individualAuthor(against) &&
				p.mintLibrivoxAuthor(cl, slug, against) {
				notes = append(notes, fmt.Sprintf("%d %q credited as %s", id, name, slug))
				return notes
			}
			p.holdLibrivoxAuthor(id, against, true)
			return notes
		}
	case lvUnsure:
		p.holdLibrivoxAuthor(id, against, false)
		return notes
	}
	if to != "" {
		credited = to
		notes = append(notes, fmt.Sprintf("%d %q credited as %q", id, name, to))
	}
	if pin := p.pinLibrivoxAuthor(cl, credited); pin != nil && to != "" {
		pin.respelled = true
	}
	return notes
}

// pinLibrivoxAuthor records the person the claim's credits name: the one the
// spelling resolves to (resolvePerson, under the batch's initials decision),
// created under that spelling when the run has to create it. A spelling that
// slugs to nothing pins nothing: the credit is dropped as unsluggable.
func (p *planner) pinLibrivoxAuthor(cl *lvAuthorClaim, spelling string) *creditPerson {
	r := p.resolvePerson(spelling)
	if r.fellBack {
		return nil
	}
	pin := pinOf(r, spelling)
	p.lv.authorPins[cl.id] = pin
	return pin
}

// mintLibrivoxAuthor pins the claim onto a person of its own at slug, a
// LibriVox author-id form (model.LibrivoxAuthorSlug for a name another person
// holds, model.LibrivoxUnslugAuthorSlug for one that slugs to nothing), named
// as LibriVox spells the credit and created when a row of the id lands; the
// stamp then writes the id that makes the slug legal. false, pinning nothing,
// when another person or a tombstone holds the slot. A split off a person of
// the same name (against, "" for an unsluggable name) is listed for a person to
// merge or confirm (Summary.LibrivoxAuthorSplits): LibriVox may list one person
// under two ids, which only a person can tell.
func (p *planner) mintLibrivoxAuthor(cl *lvAuthorClaim, slug, against string) bool {
	_, taken := p.people[slug]
	if _, retired := p.redirects.Survivor(model.RedirectPeople, slug); taken || retired {
		return false
	}
	p.lv.authorPins[cl.id] = &creditPerson{slug: slug, name: cl.name, from: slug, spelled: cl.name}
	if against != "" {
		p.lv.splits = append(p.lv.splits, lvAuthorSplit{claim: cl, slug: slug, against: against})
	}
	return true
}

// lvAuthorSplit is an author id rule 4 minted a person of its own at slug
// beside the same-name person against.
type lvAuthorSplit struct {
	claim         *lvAuthorClaim
	slug, against string
}

// LibrivoxAuthorSplit is an author id of the run filed at its author-id form
// (model.LibrivoxAuthorSlug) beside a person of the same name it is kept apart
// from (librivoxpeople.go rule 4): the other person carries another LibriVox
// author id, or only narrates, and LibriVox states no year to tell the two by.
// Only a person can tell LibriVox's own duplicate record from a namesake, so
// the pair is listed for them to merge or confirm.
type LibrivoxAuthorSplit struct {
	Author  LibrivoxPairAuthor `json:"author"`
	Against string             `json:"against"`
}

// String is the split as the import report prints it, one line:
//
//	16428 "Ralph Scott" -> ralph-scott-librivox-author-16428 (project 17170); apart from ralph-scott
func (s LibrivoxAuthorSplit) String() string {
	return fmt.Sprintf("%d %q -> %s (project %d); apart from %s", s.Author.AuthorID, s.Author.Name, s.Author.Person, s.Author.Project, s.Against)
}

// finishLibrivoxSplits lists, once every row is planned and every recording
// restated, the rule 4 splits whose person the run created - a landed row's,
// or a held row's translator the recording pass credits
// (Summary.LibrivoxAuthorSplits). One whose
// id keyLibrivoxAuthorVariants moved onto a catalogued spelling since is no
// split, and is not listed.
func (p *planner) finishLibrivoxSplits() {
	for _, sp := range p.lv.splits {
		a := p.librivoxPairAuthor(sp.claim)
		if _, made := p.people[a.Person]; made && a.Person == sp.slug {
			p.summary.LibrivoxAuthorSplits = append(p.summary.LibrivoxAuthorSplits, LibrivoxAuthorSplit{Author: a, Against: sp.against})
		}
	}
}

// noteLibrivoxFolds reports the honorific merges the import makes on LibriVox
// rows (the censuses report none there, creditCensusesOf), once every author
// and reader id is decided: a credit whose name the fold strips of a courtesy
// title and which lands on the stripped name's person - one whose id decided
// that person (librivoxCreditPerson), or one no id decides, which its name
// does. A catalogued id is its holder whatever its spelling, and a reader
// split onto a record of their own is no merge, so neither folds anything.
// Every row is read, held ones too, as the censuses would have read them.
func (p *planner) noteLibrivoxFolds(books []sourceBook) {
	for i := range books {
		b := &books[i]
		if b.lv == nil {
			continue
		}
		for _, c := range b.lv.credits {
			list, census := b.authors, p.credits.author
			if c.kind == librivoxReader {
				list, census = b.narrators, p.credits.narrator
			}
			if c.index >= len(list) {
				continue
			}
			if _, held := p.lv.people[librivoxKey{c.kind, c.id}]; held && c.id > 0 {
				continue
			}
			folded, _ := creditWithRolesSided(list[c.index], census.quiet())
			plain, _ := creditWithRolesSided(list[c.index], census.unfolded())
			if folded == plain {
				continue
			}
			if person := p.librivoxCreditPerson(c); person != "" && person != p.resolvePerson(folded).slug {
				continue
			}
			p.noteHonorific(plain, folded)
		}
	}
}

// librivoxFoldVerdict is rule 1b for a titled credit (its claim cl) whose bare
// spelling is bare: lvApart when the bare twin is another author id of the
// batch, whatever their years - two LibriVox ids are never folded into one
// person by a run; the pair is listed for a person to merge or confirm
// (Summary.LibrivoxAuthorPairs), and metacheck's honorific-person-pair
// advisory names it too - else rule 2's verdict on the catalogued person the bare
// spelling resolves to (against; "" and lvSame when it resolves to no one). An
// lvUnsure goes to the worklist as rule 2's does, rather than minting a titled
// twin of a person who may well be the author.
func (p *planner) librivoxFoldVerdict(bare string, cl *lvAuthorClaim, bySlug map[string][]*lvAuthorClaim) (against string, v lvVerdict) {
	bareSlug, fellBack := personSlug(bare)
	if fellBack {
		return "", lvSame
	}
	// The census's own veto, so the rule judges every slug the two spellings can
	// land on (planner.peopleKeptApart), as the fold it stands in for does. The slugs
	// themselves are asked too: a name that slugs to nothing has no slugs to judge.
	if p.apart.Has(cl.slug, bareSlug) || p.credits.author.reviewedApart(cl.name, bare) {
		return "", lvApart // a review recorded two people (Options.Distinct)
	}
	if others := bySlug[bareSlug]; len(others) > 0 {
		for _, other := range others {
			p.lv.pairs = append(p.lv.pairs, [2]*lvAuthorClaim{cl, other})
		}
		return "", lvApart
	}
	r := p.resolvePerson(bare)
	if r.create || r.fellBack {
		return "", lvSame
	}
	return r.slug, p.librivoxVerdict(r.slug, cl)
}

// lvVerdict is rule 2's answer about a catalogued person and an author id.
type lvVerdict int

const (
	// lvSame: nothing says they are two people; the credit lands there.
	lvSame lvVerdict = iota
	// lvApart: the catalogue says they are two (another author id, other
	// years, a narrator only, works first published long after the death).
	lvApart
	// lvUnsure: the only evidence against is recording release dates, which
	// date a modern book and a modern edition of an old one alike: a person
	// decides (the worklist).
	lvUnsure
)

// librivoxAuthorName is rules 1c and 2 for a bare credit name: the name to
// credit it by instead ("" keeps it) and the verdict on the catalogued person
// its name meets (against).
func (p *planner) librivoxAuthorName(name string, cl *lvAuthorClaim) (to, against string, v lvVerdict) {
	r := p.resolvePerson(name)
	if r.fellBack {
		return "", "", lvSame
	}
	if !r.create {
		return "", r.slug, p.librivoxVerdict(r.slug, cl)
	}
	if twin := p.librivoxTitledTwin(name, r.slug, cl.life); twin != "" {
		return twin, "", lvSame
	}
	return "", "", lvSame
}

// librivoxVerdict is rule 2's test of the catalogued person slug against an
// author id no person carries: another author id is someone else unless both
// state the same birth and death years; stated years must not disagree; and a
// person with neither is judged by their credits (librivoxCreditEvidence).
func (p *planner) librivoxVerdict(slug string, cl *lvAuthorClaim) lvVerdict {
	if v, decided := p.librivoxYearsVerdict(slug, cl.life); decided {
		return v
	}
	return p.librivoxCreditEvidence(slug, cl)
}

// librivoxYearsVerdict is rule 2's test by what the catalogued person states:
// another author id is someone else unless both state the same birth and
// death years, and stated years must not disagree. decided is false for a
// person stating neither, whom only their credits can judge.
func (p *planner) librivoxYearsVerdict(slug string, life lifeYears) (v lvVerdict, decided bool) {
	have, dated := p.lv.life[slug]
	switch {
	case p.lv.authorOf[slug] != 0:
		if sameLife(have, life) {
			return lvSame, true
		}
		return lvApart, true
	case dated:
		if lifeCompatible(have, life) {
			return lvSame, true
		}
		return lvApart, true
	}
	return lvSame, false
}

// librivoxCreditEvidence is rule 2's test for a catalogued person with no
// LibriVox author id and no years. A title in common with the projects the id
// AUTHORS (librivoxTitleKeys; a project it only translates is no evidence,
// collectLibrivoxClaims) makes them the same person. Otherwise they are apart
// when credited only as a narrator, or only on works first published (a
// stated first_published) more than librivoxLateMargin years after the claim's
// death or flourished year, and unsure when some of those works are dated only
// by their recordings' release dates. A work with no date, or one co-credited
// with someone who died within the margin, is evidence for the match, and so
// is a claim stating no year at all (nothing to measure a date against).
func (p *planner) librivoxCreditEvidence(slug string, cl *lvAuthorClaim) lvVerdict {
	works := p.librivoxPersonWorks()[slug]
	if len(works) == 0 {
		// A narrator is someone else, except a LibriVox reader meeting an id
		// that states no year: LibriVox's undated authors and translators are
		// its own volunteers.
		if p.narratorPeople[slug] && (p.lv.readerOf[slug] == 0 || cl.life.stated()) {
			return lvApart
		}
		return lvSame
	}
	bound := librivoxBound(cl.life)
	if bound == 0 {
		return lvSame
	}
	v, texts := lvApart, false
	for _, w := range works {
		if w.year == 0 || w.year <= bound || (w.died != 0 && w.died <= bound) {
			return lvSame
		}
		if w.published {
			texts = true
		} else {
			v = lvUnsure
		}
	}
	// Only now, with the dates against the match, is a title in common read.
	keys := cl.titleKeys()
	for _, w := range works {
		for k := range p.librivoxWorkKeys(w) {
			if keys[k] {
				return lvSame
			}
		}
	}
	if v == lvUnsure && p.librivoxReleasesAreEditions(cl, bound) {
		// The release dates are shown to date editions, not texts: only a
		// text date past the bound still says someone else.
		if texts {
			return lvApart
		}
		return lvSame
	}
	return v
}

// librivoxBound is the last year a work may first appear and still be evidence
// for an author of life: the end of their life (lifeEnd: a birth year alone
// bounds it too) or their flourished year, plus librivoxLateMargin; 0 when
// life states no year.
func librivoxBound(life lifeYears) int {
	end := cmp.Or(life.death, life.fl, lifeEnd(life))
	if end == 0 {
		return 0
	}
	return end + librivoxLateMargin
}

// librivoxReleasesAreEditions reports whether the claim's own work shows that
// the catalogue's release dates date modern editions of its texts, not the
// texts: a project the id AUTHORS (a translation is another author's book:
// Thomas Taylor's 1816 Proclus, catalogued as Proclus's own, says nothing of a
// modern Thomas Taylor's novels), which LibriVox dates by its text within the
// bound ("Narrative of Sojourner Truth", 1850), and which the catalogue holds
// under the same full title (librivoxTitleKey, a qualifier and all:
// "Metamorphoses (Howard Version)" names one translation, not Ovid's book; a
// generic title, librivoxGenericWordsOnly, names none) as
// a work dated only by a release past the bound (2009), credited to a
// catalogued person the project credits beside the id (coAuthors, by slug:
// `olive-gilbert`, never just a shared surname, which the id's own name is
// enough to supply - Mary Webb's "Garden Verses" says nothing beside a modern
// Precious Webb's). A same-name person dated only by such releases is then no
// evidence against the match. Only a project of at most librivoxEditionAuthors
// authors shows it: in an anthology (Carolyn Wells and a hundred poets) an
// edition credited to one contributor says nothing of every other's text.
func (p *planner) librivoxReleasesAreEditions(cl *lvAuthorClaim, bound int) bool {
	for _, pr := range cl.projects {
		if !pr.own || pr.authors > librivoxEditionAuthors || pr.textYear == 0 || pr.textYear > bound {
			continue
		}
		// A generic title ("Poems") is no title in common here either: the
		// same-name person's own "Poems" says nothing about whose text it is.
		slugs, nonLatin := p.librivoxTitleWorksAll(pr.title)
		if nonLatin || librivoxGenericWordsOnly(pr.title) {
			continue
		}
		for _, slug := range slugs {
			w, ok := p.librivoxCatalogueWorks()[slug]
			if !ok || w.published || w.year <= bound {
				continue
			}
			for _, person := range w.people {
				if pr.coAuthors[person] {
					return true
				}
			}
		}
	}
	return false
}

// librivoxEditionAuthors is the most authors a project may credit for an
// edition of it to speak for the id (librivoxReleasesAreEditions): a book of
// one or two writers ("Narrative of Sojourner Truth": Truth and Olive
// Gilbert), never an anthology, where one contributor's edition would clear
// the evidence against every other.
const librivoxEditionAuthors = 2

// lvClaimProject is one project of a claim, as librivoxReleasesAreEditions
// reads it: its title, the year LibriVox dates its text by (0 = none), the
// catalogued people its OTHER author credits are (coAuthors), how many authors
// it credits (translators aside), and whether the claim authors it (own)
// rather than translating it.
type lvClaimProject struct {
	title     string
	textYear  int
	coAuthors map[string]bool
	authors   int
	own       bool
}

// librivoxTitledTwin is rule 1c for the bare credit name at slug: the catalogued
// name of the ONE person whose name is that slug behind a courtesy title or a
// rank, unless a review keeps the two apart (peopleKeptApart), when the twin is
// not credited only as a narrator (rule 2's narrator test), the life years do
// not disagree and, if the twin carries an author id, the credit states no
// years of its own or the same birth and death; "" otherwise.
func (p *planner) librivoxTitledTwin(name, slug string, life lifeYears) string {
	twins := p.librivoxTitledTwins()[slug]
	if len(twins) != 1 {
		return ""
	}
	twin := twins[0]
	// The slugs themselves are asked as well as the names: a missing name has no
	// slugs to judge (creditSlugs), and that must not skip the review.
	if p.apart.Has(slug, twin) || p.peopleKeptApart(name, p.people[twin]) {
		return "" // a review recorded two people (Options.Distinct)
	}
	if p.narratorPeople[twin] && len(p.librivoxPersonWorks()[twin]) == 0 {
		return ""
	}
	tl := p.lv.life[twin]
	if !lifeCompatible(tl, life) {
		return ""
	}
	if p.lv.authorOf[twin] != 0 && life.stated() && !sameLife(tl, life) {
		return ""
	}
	return p.people[twin]
}

// librivoxTitledTwins indexes the catalogue's titled names (a courtesy title
// or a rank, deTitled) by the slug of the bare name, built on first use, while
// p.people still holds the catalogue alone. Only a record at its own name's
// slug is indexed: rule 1c credits the twin through its NAME (the spelling
// pinLibrivoxAuthor resolves), which reaches no other record (a
// `-librivox-<id>` reader's).
func (p *planner) librivoxTitledTwins() map[string][]string {
	if p.lv.titledTwins != nil {
		return p.lv.titledTwins
	}
	p.lv.titledTwins = map[string][]string{}
	for slug, name := range p.people {
		if own, _ := personSlug(name); own != slug {
			continue
		}
		if bare, ok := deTitled(name); ok {
			if bs, fellBack := personSlug(bare); !fellBack && bs != slug {
				p.lv.titledTwins[bs] = append(p.lv.titledTwins[bs], slug)
			}
		}
	}
	for bs := range p.lv.titledTwins {
		slices.Sort(p.lv.titledTwins[bs])
	}
	return p.lv.titledTwins
}

// librivoxDatedName is the name an author id is minted under when its plain
// name is someone else's (rule 2): the name with its LibriVox years, provided
// that name is not a person carrying another author id; "" when there is none.
func (p *planner) librivoxDatedName(id int, name string, life lifeYears) string {
	dated := datedName(name, life)
	if dated == "" {
		return ""
	}
	r := p.resolvePerson(dated)
	if r.fellBack || (!r.create && p.lv.authorOf[r.slug] != 0 && p.lv.authorOf[r.slug] != id) {
		return ""
	}
	return dated
}

// holdLibrivoxAuthor sends an author id's rows to the worklist
// (same-name-author), naming the works of the person its name meets (against):
// a catalogued person's now, a person of this run (deferred) once the run has
// planned them.
func (p *planner) holdLibrivoxAuthor(id int, against string, deferred bool) {
	h := lvHold{against: against, candidates: []LibrivoxCandidate{}, deferred: deferred}
	if against != "" && !deferred {
		h.candidates = p.librivoxPersonCandidates(against)
	}
	p.lv.sameName[id] = h
}

// librivoxPersonCandidates is the works of a catalogued person as a
// same-name-author worklist row names them (firstCandidates).
func (p *planner) librivoxPersonCandidates(slug string) []LibrivoxCandidate {
	out := []LibrivoxCandidate{}
	for _, w := range p.librivoxPersonWorks()[slug] {
		out = append(out, LibrivoxCandidate{Work: w.slug, Title: w.title})
	}
	return firstCandidates(out)
}

// firstCandidates is a same-name-author row's candidates as the worklist
// names them: the first maxWarnExamples. (The work-matching reasons name every
// work they weigh, candidatesOf, so a person can see the whole tie.)
func firstCandidates(c []LibrivoxCandidate) []LibrivoxCandidate {
	return c[:min(len(c), maxWarnExamples)]
}

// yearText is a signed year as the run prints one: "1883", "43 BC".
func yearText(y int) string {
	if y < 0 {
		return strconv.Itoa(-y) + " BC"
	}
	return strconv.Itoa(y)
}

// datedName is name with life's years in the form the tranches minted by hand:
// "(1822-1887)", "(d. 1942)", "(b. 1850)", "(fl. 1916)"; a year BC reads "43 BC".
// "" when no year is stated.
func datedName(name string, life lifeYears) string {
	switch {
	case life.birth != 0 && life.death != 0:
		return fmt.Sprintf("%s (%s-%s)", name, yearText(life.birth), yearText(life.death))
	case life.death != 0:
		return fmt.Sprintf("%s (d. %s)", name, yearText(life.death))
	case life.birth != 0:
		return fmt.Sprintf("%s (b. %s)", name, yearText(life.birth))
	case life.fl != 0:
		return fmt.Sprintf("%s (fl. %d)", name, life.fl)
	}
	return ""
}

// lifeCompatible reports whether no year stated on both sides disagrees: a
// birth or death year both state, or a flourished year outside the other's
// life ("fl. 1916" is not someone born in 1920).
func lifeCompatible(a, b lifeYears) bool {
	return (a.birth == 0 || b.birth == 0 || a.birth == b.birth) &&
		(a.death == 0 || b.death == 0 || a.death == b.death) &&
		flourishedWithin(a.fl, b) && flourishedWithin(b.fl, a)
}

// flourishedWithin reports whether a flourished year (0 = none) falls inside
// life's stated birth and death years.
func flourishedWithin(fl int, life lifeYears) bool {
	return fl == 0 || ((life.birth == 0 || fl >= life.birth) && (life.death == 0 || fl <= life.death))
}

// sameLife reports whether both state the same birth AND death year: what
// lets two LibriVox author ids be one person.
func sameLife(a, b lifeYears) bool {
	return a.birth != 0 && a.death != 0 && a.birth == b.birth && a.death == b.death
}

// librivoxAuthorRecord is rule 3's test: the person slug is an author record
// no LibriVox reader is stamped onto - one carrying a catalogued
// librivox_author_id, one an author id of the run whose years end before
// LibriVox began lands on (runAuthors, settleLibrivoxPeople, so a reader met
// first is split from them as one met after), or one whose catalogued years
// end so. It is asked only before any row is planned (decideLibrivoxReader),
// so authorOf holds the catalogue's ids alone: an id a row of this run claims
// (claimLibrivoxAuthors) is claimed as rows land, and counting it would split
// a reader from an undated author when the author's row came first and not
// otherwise.
func (p *planner) librivoxAuthorRecord(slug string) bool {
	if p.lv.authorOf[slug] != 0 || p.lv.runAuthors[slug] {
		return true
	}
	end := lifeEnd(p.lv.life[slug])
	return end != 0 && end < librivoxFounded
}

// librivoxClaimProject is a row (planned, or a held row for its translators)
// as a claim's project (claim the
// claim's credit on it, people the row's librivoxCreditPeople): its other
// author credits' catalogued people.
func librivoxClaimProject(b *sourceBook, claim librivoxCredit, people []string) lvClaimProject {
	pr := lvClaimProject{title: b.str("title"), textYear: b.lv.copyrightYear, coAuthors: map[string]bool{}, own: claim.suffix == ""}
	for j, c := range b.lv.credits {
		if c.kind != librivoxAuthor {
			continue
		}
		if c.suffix == "" {
			pr.authors++
		}
		if c.index != claim.index && c.id != claim.id && people[j] != "" {
			pr.coAuthors[people[j]] = true
		}
	}
	return pr
}

// librivoxCreditPeople is the catalogued person of each of a row's author and
// translator credits, by credit ("" for none): a catalogued id's holder, else
// the live person the census-cleaned name resolves to, unless the years that
// person states (or another author id they carry) say they are not this
// credit (librivoxYearsVerdict). It is read without any of the run's
// decisions, so a claim is judged alike whichever co-author the run decides
// first.
func (p *planner) librivoxCreditPeople(b *sourceBook, quiet creditCensus) []string {
	people := make([]string, len(b.lv.credits))
	for j, c := range b.lv.credits {
		if c.kind != librivoxAuthor || c.index >= len(b.authors) {
			continue
		}
		if owner, held := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; held && c.id > 0 {
			if live, ok := p.livePerson(owner); ok {
				people[j] = live
			}
			continue
		}
		name, _ := creditWithRolesSided(b.authors[c.index], quiet)
		if r := p.resolvePerson(name); !r.create && !r.fellBack {
			if v, decided := p.librivoxYearsVerdict(r.slug, c.life); !decided || v == lvSame {
				people[j] = r.slug
			}
		}
	}
	return people
}

// librivoxSameNameSkip puts a row crediting an author id rule 2 could not
// tell apart on the worklist (same-name-author), naming the works of the
// person its name meets. false for every other row.
// It is asked before routeLibrivox, so no attach is counted for a row it skips.
func (p *planner) librivoxSameNameSkip(b sourceBook, title string) bool {
	h, ok := p.librivoxSameNameOf(b)
	if ok {
		if h.deferred {
			p.lv.pendingHolds = append(p.lv.pendingHolds, lvPending{entry: len(p.summary.LibrivoxWorklist), against: h.against})
		}
		p.noteLibrivoxWorklist(b, title, LibrivoxSameNameAuthor, h.candidates)
	}
	return ok
}

// librivoxSameNameOf is the hold of the row's first author id rule 2 sent to
// the worklist; ok=false for every other row.
func (p *planner) librivoxSameNameOf(b sourceBook) (h lvHold, ok bool) {
	if b.lv == nil || len(p.lv.sameName) == 0 {
		return lvHold{}, false
	}
	for _, c := range b.lv.credits {
		if h, ok := p.lv.sameName[c.id]; ok && c.kind == librivoxAuthor {
			return h, true
		}
	}
	return lvHold{}, false
}

// lvPending is a same-name-author worklist row (its index) whose candidates are
// the works of a person this run lands another id on (lvHold.deferred).
type lvPending struct {
	entry   int
	against string
}

// finishLibrivoxHolds names, on each deferred same-name-author worklist row,
// the works the person its name meets is credited on once the run has planned
// every row - what a later run, with that person catalogued, would name.
func (p *planner) finishLibrivoxHolds() {
	if len(p.lv.pendingHolds) == 0 {
		return
	}
	works := map[string][]LibrivoxCandidate{}
	for _, h := range p.lv.pendingHolds {
		works[h.against] = nil
	}
	for slug, ws := range p.works {
		for against := range works {
			if p.workCreditsPerson(slug, against) {
				works[against] = append(works[against], LibrivoxCandidate{Work: slug, Title: ws.title})
			}
		}
	}
	for against, c := range works {
		slices.SortFunc(c, func(a, b LibrivoxCandidate) int { return strings.Compare(a.Work, b.Work) })
		works[against] = firstCandidates(c)
	}
	for _, h := range p.lv.pendingHolds {
		if c := works[h.against]; c != nil {
			p.summary.LibrivoxWorklist[h.entry].Candidates = c
		}
	}
}

// librivoxCatalogueWorks is librivoxPersonWorks' works by slug.
func (p *planner) librivoxCatalogueWorks() map[string]lvPersonWork {
	p.librivoxPersonWorks()
	return p.lv.workDates
}

// librivoxPersonWorks indexes the catalogue's works by every person they
// credit (authors and credits[]), each with its first-publication year, built
// on first use from the catalogue load; resolveLibrivoxAuthors, its only
// reader, lets it go.
func (p *planner) librivoxPersonWorks() map[string][]lvPersonWork {
	if p.lv.works != nil {
		return p.lv.works
	}
	p.lv.works, p.lv.workDates = map[string][]lvPersonWork{}, map[string]lvPersonWork{}
	if p.catalog == nil {
		return p.lv.works
	}
	for _, w := range p.catalog.Works {
		people := slices.Clone(w.Authors)
		for _, c := range w.Credits {
			people = append(people, c.Person)
		}
		slices.Sort(people)
		people = slices.Compact(people)
		pw := lvPersonWork{slug: w.ID, title: w.Title, people: people}
		pw.year, pw.published = workFirstYear(w)
		p.lv.workDates[w.ID] = pw
		for _, person := range people {
			if d := p.lv.life[person].death; d != 0 && (pw.died == 0 || d < pw.died) {
				pw.died = d
			}
		}
		for _, person := range people {
			p.lv.works[person] = append(p.lv.works[person], pw)
		}
	}
	return p.lv.works
}

// workFirstYear is the year a work first appeared as far as the catalogue
// says: its text's date (published) - its first_published, else the earliest
// copyright year a LibriVox recording of it states - else the earliest
// release date of its recordings; 0 when it states none.
func workFirstYear(w *model.Work) (year int, published bool) {
	if y := leadingYear(w.FirstPublished); y != 0 {
		return y, true
	}
	for _, r := range w.Recordings {
		if y := r.CopyrightYear; r.LibrivoxID != 0 && y != 0 && (year == 0 || y < year) {
			year = y
		}
	}
	if year != 0 {
		return year, true
	}
	for _, r := range w.Recordings {
		if y := leadingYear(r.ReleaseDate); y != 0 && (year == 0 || y < year) {
			year = y
		}
	}
	return year, false
}

// leadingYear reads the four-digit year a date string starts with (YearOf),
// 0 when none.
func leadingYear(s string) int {
	y, _ := strconv.Atoi(YearOf(s))
	return y
}
