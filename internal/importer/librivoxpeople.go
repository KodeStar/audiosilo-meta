package importer

// librivoxpeople.go is the LibriVox import's three PERSON rules (meta#2778),
// the classes LibriVox tranches 7 to 11 fixed by hand in every tranche. Each is
// decided per LibriVox author id before any census reads a name
// (decideLibrivoxAuthors, called from keyLibrivoxCredits), by respelling the
// id's credits, because a person's identity here IS the slug of the name it is
// credited by: whatever a credit is spelled as is who it lands on.
//
//  1. THE FOLD. The honorific fold (honorific.go) strips a courtesy title when
//     the bare name is a credit too. On a LibriVox row the ids say more than
//     the census does, so:
//     a. a credit whose id the catalogue holds is spelled as its holder and
//     never folded (keepTitled): the id, not the spelling, is the person, so
//     "Mrs. C. M. Livingston" (1456) stays on `mrs-c-m-livingston`;
//     b. a titled credit is not folded onto a bare twin that is another
//     LibriVox author id of the batch with other life years ("Mrs. William
//     Platt" 14096 beside "William Platt" 13717, one project), nor onto a
//     catalogued bare record that is another person by rule 2's test ("Sir
//     Hugh Fraser" is not `hugh-fraser`, the actor who narrates Poirot), and
//     when that test is unsure the row goes to the worklist as rule 2's does;
//     a titled name the fold leaves meets rule 2 under its own spelling;
//     c. a bare credit about to mint a new record resolves onto the ONE
//     catalogued twin carrying a courtesy title or a military rank
//     (librivoxTitledTwin): "Frederick Treves" onto `sir-frederick-treves`,
//     "Joseph H. Alexander" onto `colonel-joseph-h-alexander`, and LibriVox's
//     undated duplicate record "C. M. Livingston" (15071) onto
//     `mrs-c-m-livingston`. Not when the twin only narrates, nor when the
//     life years disagree, nor when the twin carries another author id and
//     the credit states years of its own.
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
//     death (or flourished year), with no title in common with the id's
//     projects (librivoxCreditEvidence).
//     A work's first publication is its first_published, else the earliest
//     release date of its recordings, and a work co-credited with someone who
//     died within the margin is an old text whatever its recordings say; a
//     work with no date at all is no evidence against the match. Instead the id is minted at its DATED name
//     (librivoxDatedName: "James Grant (1822-1887)" -> `james-grant-1822-1887`,
//     "Richard Wilson (fl. 1916)" -> `richard-wilson-fl-1916`), the form the
//     tranches minted by hand; when LibriVox states no year its rows go to the
//     worklist as `same-name-author` (Ralph Scott), never stamped silently. Two
//     ids of the batch meeting one free name are decided the same way: the
//     first keeps it, a second whose years disagree is dated.
//  3. READERS ON AUTHORS. A reader whose name meets a catalogued person holding
//     no reader id claims them, unless that person is an AUTHOR record
//     (librivoxAuthorRecord: a librivox_author_id, or a death year before
//     LibriVox began, librivoxFounded, its own or that of an author id of the
//     run landing on it, whatever the row order): the reader is then a person
//     of their own at `<name>-librivox-<reader id>`, as for a second reader of
//     one name (`adam-smith-librivox-19341`, not Adam Smith 1723-1790
//     narrating a 2023 recording). In librivoxReaderSlug.
//
// A project the catalogue already holds is never decided here (it is skipped
// before planning; its readers are librivoxReaderSlug's), so a rerun of a
// landed tranche changes nothing.

import (
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
	// published says year is the work's stated first_published, not the
	// earliest release of its recordings.
	published bool
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
// catalogued author is the LibriVox one, whatever their works' dates say.
func librivoxTitleKeys(title string) map[string]bool {
	keys := map[string]bool{}
	for t := title; t != ""; {
		if k := librivoxTitleKey(t); k != "" {
			keys[k] = true
		}
		stripped := stripTrailingQualifier(t)
		if stripped == t {
			break
		}
		t = stripped
	}
	if k := titlerule.IdentityTitleKey(title, ""); k != "" {
		keys["identity:"+k] = true
	}
	return keys
}

// lvAuthorClaim is one LibriVox author id as the batch's rows to be planned
// state it: its cleaned name and the slug of it ("" on the catch-all), life
// years and the titles of its projects, keyed on first use (titleKeys).
type lvAuthorClaim struct {
	name, slug string
	life       lifeYears
	titles     []string
	keys       map[string]bool
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

// decideLibrivoxAuthors applies rules 1 and 2 to every author id the
// catalogue does not hold, over the rows that will be planned (a held project
// is skipped and decides nothing), in the order the ids first appear, and
// respells their credits. It runs inside keyLibrivoxCredits, after the
// catalogued ids are keyed and before the censuses read a name.
func (p *planner) decideLibrivoxAuthors(books []sourceBook) {
	claims := map[int]*lvAuthorClaim{}
	var order []*lvAuthorClaim
	var ids []int
	bySlug := map[string][]*lvAuthorClaim{}
	for i := range books {
		b := &books[i]
		if b.lv == nil || p.lv.ids[b.lv.id] {
			continue
		}
		for _, c := range b.lv.credits {
			if c.kind != librivoxAuthor || c.id <= 0 {
				continue
			}
			if _, held := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; held {
				continue
			}
			cl := claims[c.id]
			if cl == nil {
				cl = &lvAuthorClaim{name: CleanCreditName(c.name), life: c.life}
				if slug, fellBack := personSlug(cl.name); !fellBack {
					cl.slug = slug // librivoxauthors.go rule 2: never on the catch-all
					bySlug[slug] = append(bySlug[slug], cl)
				}
				claims[c.id] = cl
				order = append(order, cl)
				ids = append(ids, c.id)
			}
			cl.titles = append(cl.titles, b.str("title"))
		}
	}
	if len(order) == 0 {
		return
	}
	// The indexes below are read only here.
	defer func() { p.lv.works, p.lv.titledTwins = nil, nil }()
	renames := map[int]string{}
	holder := map[string]*lvAuthorClaim{}
	var decided []string
	for i, cl := range order {
		id, name, slug := ids[i], cl.name, cl.slug
		if slug == "" {
			continue
		}
		if bare, titled := deHonorified(name); titled {
			against, v := p.librivoxFoldVerdict(bare, cl, bySlug)
			switch {
			case v == lvUnsure:
				p.holdLibrivoxAuthor(id, against)
				continue
			case v == lvApart:
				p.lv.keepTitled[slug] = true
				decided = append(decided, fmt.Sprintf("%d %q kept apart from %q", id, name, bare))
			case against != "":
				// Folds onto the catalogued bare person the verdict accepted.
				p.noteDeadAuthor(cl, against)
				continue
			}
			// The titled name itself still meets rule 2 below.
		}
		to, against, v := p.librivoxAuthorName(name, cl)
		if v == lvSame && to == "" {
			if h, ok := holder[slug]; ok && !lifeCompatible(h.life, cl.life) {
				v = lvApart
			} else if !ok {
				holder[slug] = cl
			}
		}
		switch v {
		case lvApart:
			if to = p.librivoxDatedName(id, name, cl.life); to == "" {
				p.holdLibrivoxAuthor(id, against)
				continue
			}
		case lvUnsure:
			p.holdLibrivoxAuthor(id, against)
			continue
		}
		credited := name
		if to != "" {
			credited = to
			renames[id] = to
			decided = append(decided, fmt.Sprintf("%d %q credited as %q", id, name, to))
			if _, titled := deHonorified(to); titled {
				p.lv.keepTitled[Slugify(to)] = true
			}
		}
		if r := p.resolvePerson(credited); !r.fellBack {
			p.noteDeadAuthor(cl, r.slug)
		}
	}
	for i := range books {
		b := &books[i]
		if b.lv == nil || p.lv.ids[b.lv.id] {
			continue
		}
		for j := range b.lv.credits {
			c := &b.lv.credits[j]
			if to, ok := renames[c.id]; ok && c.kind == librivoxAuthor {
				c.name = to
				b.authors[c.index] = to + c.suffix
			}
		}
	}
	if len(decided) > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"%d LibriVox author id(s) kept off a same-name or courtesy-title twin, or credited by another name (the rest the person rules held are on the worklist as same-name-author): %s",
			len(decided), strings.Join(decided, "; ")))
	}
}

// librivoxFoldVerdict is rule 1b for a titled credit (its claim cl) whose bare
// spelling is bare: lvApart when the bare twin is another id of the batch with
// other life years, else rule 2's verdict on the catalogued person the bare
// spelling resolves to (against; "" and lvSame when it resolves to no one). An
// lvUnsure goes to the worklist as rule 2's does, rather than minting a titled
// twin of a person who may well be the author.
func (p *planner) librivoxFoldVerdict(bare string, cl *lvAuthorClaim, bySlug map[string][]*lvAuthorClaim) (against string, v lvVerdict) {
	bareSlug, fellBack := personSlug(bare)
	if fellBack {
		return "", lvSame
	}
	for _, other := range bySlug[bareSlug] {
		if !sameLife(other.life, cl.life) {
			return "", lvApart
		}
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
	if twin := p.librivoxTitledTwin(r.slug, cl.life); twin != "" {
		return twin, "", lvSame
	}
	return "", "", lvSame
}

// librivoxVerdict is rule 2's test of the catalogued person slug against an
// author id no person carries: another author id is someone else unless both
// state the same birth and death years; stated years must not disagree; and a
// person with neither is judged by their credits (librivoxCreditEvidence).
func (p *planner) librivoxVerdict(slug string, cl *lvAuthorClaim) lvVerdict {
	life, dated := p.lv.life[slug]
	switch {
	case p.lv.authorOf[slug] != 0:
		if sameLife(life, cl.life) {
			return lvSame
		}
		return lvApart
	case dated:
		if lifeCompatible(life, cl.life) {
			return lvSame
		}
		return lvApart
	}
	return p.librivoxCreditEvidence(slug, cl)
}

// librivoxCreditEvidence is rule 2's test for a catalogued person with no
// LibriVox author id and no years. A title in common with the id's projects
// (librivoxTitleKeys) makes them the same person. Otherwise they are apart
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
	bound := cl.life.death
	if bound == 0 {
		bound = cl.life.fl
	}
	if bound == 0 {
		return lvSame
	}
	bound += librivoxLateMargin
	v := lvApart
	for _, w := range works {
		if w.year == 0 || w.year <= bound || (w.died != 0 && w.died <= bound) {
			return lvSame
		}
		if !w.published {
			v = lvUnsure
		}
	}
	// Only now, with the dates against the match, is a title in common read.
	keys := cl.titleKeys()
	for _, w := range works {
		for k := range librivoxTitleKeys(w.title) {
			if keys[k] {
				return lvSame
			}
		}
	}
	return v
}

// librivoxTitledTwin is rule 1c: the catalogued name of the ONE person whose
// name is the bare slug behind a courtesy title or a rank, when the twin is
// not credited only as a narrator (rule 2's narrator test), the life years do
// not disagree and, if the twin carries an author id, the credit states no
// years of its own or the same birth and death; "" otherwise.
func (p *planner) librivoxTitledTwin(slug string, life lifeYears) string {
	twins := p.librivoxTitledTwins()[slug]
	if len(twins) != 1 {
		return ""
	}
	twin := twins[0]
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
// slug is indexed: rule 1c respells the credit by the twin's NAME, which
// reaches no other record (a `-librivox-<id>` reader's).
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
// (same-name-author), naming the works of the catalogued person its name
// meets (against; none for another id of the batch).
func (p *planner) holdLibrivoxAuthor(id int, against string) {
	candidates := []LibrivoxCandidate{}
	if against != "" {
		for _, w := range p.librivoxPersonWorks()[against] {
			candidates = append(candidates, LibrivoxCandidate{Work: w.slug, Title: w.title})
			if len(candidates) == maxWarnExamples {
				break
			}
		}
	}
	p.lv.sameName[id] = candidates
}

// datedName is name with life's years in the form the tranches minted by hand:
// "(1822-1887)", "(d. 1942)", "(b. 1850)", "(fl. 1916)"; a year BC reads "43 BC".
// "" when no year is stated.
func datedName(name string, life lifeYears) string {
	year := func(y int) string {
		if y < 0 {
			return strconv.Itoa(-y) + " BC"
		}
		return strconv.Itoa(y)
	}
	switch {
	case life.birth != 0 && life.death != 0:
		return fmt.Sprintf("%s (%s-%s)", name, year(life.birth), year(life.death))
	case life.death != 0:
		return fmt.Sprintf("%s (d. %s)", name, year(life.death))
	case life.birth != 0:
		return fmt.Sprintf("%s (b. %s)", name, year(life.birth))
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

// librivoxAuthorRecord is rule 3's test: the catalogued (or claimed) person
// slug is an author record no LibriVox reader is stamped onto. A person an
// author id of the run who died before LibriVox began lands on (deadAuthors)
// is one before its row is planned, so a reader met first is split from them
// as one met after.
func (p *planner) librivoxAuthorRecord(slug string) bool {
	if p.lv.authorOf[slug] != 0 || p.lv.deadAuthors[slug] {
		return true
	}
	d := p.lv.life[slug].death
	return d != 0 && d < librivoxFounded
}

// noteDeadAuthor records that the claim cl lands on slug, for rule 3, when its
// years end before LibriVox began.
func (p *planner) noteDeadAuthor(cl *lvAuthorClaim, slug string) {
	if d := cl.life.death; d != 0 && d < librivoxFounded {
		p.lv.deadAuthors[slug] = true
	}
}

// librivoxSameNameSkip puts a row crediting an author id rule 2 could not
// tell apart on the worklist (same-name-author), naming the works of the
// person its name meets. false for every other row.
// It is asked before routeLibrivox, so no attach is counted for a row it skips.
func (p *planner) librivoxSameNameSkip(b sourceBook, title string) bool {
	candidates, ok := p.librivoxSameNameOf(b)
	if ok {
		p.noteLibrivoxWorklist(b, title, LibrivoxSameNameAuthor, candidates)
	}
	return ok
}

// librivoxSameNameOf is the candidates of the row's first author id rule 2
// sent to the worklist; ok=false for every other row.
func (p *planner) librivoxSameNameOf(b sourceBook) (candidates []LibrivoxCandidate, ok bool) {
	if b.lv == nil || len(p.lv.sameName) == 0 {
		return nil, false
	}
	for _, c := range b.lv.credits {
		if candidates, ok := p.lv.sameName[c.id]; ok && c.kind == librivoxAuthor {
			return candidates, true
		}
	}
	return nil, false
}

// librivoxPersonWorks indexes the catalogue's works by every person they
// credit (authors and credits[]), each with its first-publication year, built
// on first use from the catalogue load; decideLibrivoxAuthors, its only
// reader, lets it go.
func (p *planner) librivoxPersonWorks() map[string][]lvPersonWork {
	if p.lv.works != nil {
		return p.lv.works
	}
	p.lv.works = map[string][]lvPersonWork{}
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
		pw := lvPersonWork{slug: w.ID, title: w.Title}
		pw.year, pw.published = workFirstYear(w)
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
// says: its first_published (published), else the earliest release date of its
// recordings; 0 when it states neither.
func workFirstYear(w *model.Work) (year int, published bool) {
	if y := leadingYear(w.FirstPublished); y != 0 {
		return y, true
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
