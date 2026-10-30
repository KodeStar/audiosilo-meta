package importer

// The three person rules of librivoxpeople.go (meta#2778), each from the real
// cases LibriVox tranches 7 to 11 fixed by hand, beside the genuine match each
// rule must keep.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// lvDated is a LibriVox author credit with its dob and dod as the feed states
// them ("1853", "", "fl.1916").
func lvDated(id int, first, last, dob, dod string) LibrivoxPerson {
	return LibrivoxPerson{ID: lvInt(id), FirstName: first, LastName: last, DOB: lvText(dob), DOD: lvText(dod)}
}

// byAuthors is byAuthor with several authors, in order.
func byAuthors(t *testing.T, id int, title string, authors ...LibrivoxPerson) LibrivoxProject {
	t.Helper()
	p := byAuthor(t, id, title, "English", authors[0])
	p.Authors = authors
	return p
}

// lvPersonFull is a person record with the fields the person rules write.
type lvPersonFull struct {
	Name      string `json:"name"`
	ReaderID  int    `json:"librivox_reader_id"`
	AuthorID  int    `json:"librivox_author_id"`
	BirthYear int    `json:"birth_year"`
	DeathYear int    `json:"death_year"`
}

func personOf(t *testing.T, dataDir, slug string) lvPersonFull {
	t.Helper()
	var p lvPersonFull
	readEntity(t, dataDir, personAddr(slug), &p)
	return p
}

// workAuthorsOf reads one work's authors.
func workAuthorsOf(t *testing.T, dataDir, slug string) []string {
	t.Helper()
	var w lvWork
	readEntity(t, dataDir, workAddr(slug), &w)
	return w.Authors
}

// personFiles is a person record carrying fields.
func personFiles(t *testing.T, slug, name string, fields map[string]any) string {
	t.Helper()
	return testpack.WithFields(t, testpack.PersonJSON(t, slug, name), fields)
}

// retailWork is a catalogued work by author (one recording read by
// someone-else, released 2020: testpack's default), first_published when set.
func retailWork(t *testing.T, files map[string]string, slug, title, author, firstPublished string) {
	t.Helper()
	w := testpack.WorkJSON(t, slug, title, testpack.WithAuthors(author))
	if firstPublished != "" {
		w = testpack.WithField(t, w, "first_published", firstPublished)
	}
	files[workAddr(slug)] = w
	files[recAddr(slug, "se")] = testpack.RecJSON(t, "se", slug, testpack.WithNarrators("someone-else"))
	files[personAddr("someone-else")] = testpack.PersonJSON(t, "someone-else", "Someone Else")
}

// narratedWork is a catalogued work by someone-else read by narrator.
func narratedWork(t *testing.T, files map[string]string, slug, title, narrator string) {
	t.Helper()
	files[workAddr(slug)] = testpack.WorkJSON(t, slug, title, testpack.WithAuthors("someone-else"))
	files[recAddr(slug, "rec")] = testpack.RecJSON(t, "rec", slug, testpack.WithNarrators(narrator))
	files[personAddr("someone-else")] = testpack.PersonJSON(t, "someone-else", "Someone Else")
}

// rerunIsANoOp runs snap into dataDir again and fails if anything changes.
func rerunIsANoOp(t *testing.T, snap, dataDir string) {
	t.Helper()
	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewPeople+again.NewRecordings+again.LibrivoxIDsStamped != 0 || len(again.Files) != 0 {
		t.Errorf("rerun = %+v, want a no-op", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

// Rule 1: the fold.

func TestLibrivoxFoldKeepsTwoAuthorIDsApart(t *testing.T) {
	// 13419 "Stories of the Scottish Border": William Platt (13717) and Mrs.
	// William Platt (14096) are two LibriVox authors; the fold made her him.
	snap := writeSnapshot(t, byAuthors(t, 13419, "Stories of the Scottish Border",
		lvDated(13717, "William ", "Platt", "1867", "1943"), lvDated(14096, "Mrs. William", "Platt", "", "1942")))
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "stories-of-the-scottish-border"); !reflect.DeepEqual(got, []string{"william-platt", "mrs-william-platt"}) {
		t.Fatalf("authors = %v\nwarnings: %v", got, sum.Warnings)
	}
	if p := personOf(t, dataDir, "mrs-william-platt"); p.AuthorID != 14096 || p.DeathYear != 1942 || p.Name != "Mrs. William Platt" {
		t.Errorf("mrs-william-platt = %+v", p)
	}
	if p := personOf(t, dataDir, "william-platt"); p.AuthorID != 13717 {
		t.Errorf("william-platt = %+v", p)
	}
	if len(sum.HonorificMerges) != 0 {
		t.Errorf("HonorificMerges = %v, want none", sum.HonorificMerges)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxCataloguedIDHolderIsNeverFolded(t *testing.T) {
	// 14501 "Sevenfold Trouble" credits LibriVox's two records of one author:
	// 1456 "Mrs. C. M. Livingston" (catalogued, 1832-1924) and the undated
	// 15071 "C. M. Livingston". The id holder keeps the credit, and the
	// undated duplicate lands on her too rather than minting c-m-livingston.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("mrs-c-m-livingston"): personFiles(t, "mrs-c-m-livingston", "Mrs. C. M. Livingston",
			map[string]any{"librivox_author_id": 1456, "birth_year": 1832, "death_year": 1924}),
	})
	snap := writeSnapshot(t, byAuthors(t, 14501, "Sevenfold Trouble",
		lvDated(743, "Grace Livingston", "Hill", "1865", "1947"),
		lvDated(1456, "Mrs. C. M.", "Livingston", "1832", "1924"),
		lvDated(15071, "C. M.", "Livingston", "", "")))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "sevenfold-trouble"); !reflect.DeepEqual(got, []string{"grace-livingston-hill", "mrs-c-m-livingston"}) {
		t.Fatalf("authors = %v\nnotes: %v", got, sum.Notes)
	}
	if entryExists(t, dataDir, personAddr("c-m-livingston")) {
		t.Error("an undated c-m-livingston was minted")
	}
	if p := personOf(t, dataDir, "mrs-c-m-livingston"); p.AuthorID != 1456 {
		t.Errorf("mrs-c-m-livingston = %+v", p)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxBareAuthorResolvesOntoATitledTwin(t *testing.T) {
	// 13964: a bare "Frederick Treves" minted frederick-treves beside the
	// catalogued sir-frederick-treves; 16256: "Joseph H. Alexander" beside
	// colonel-joseph-h-alexander (a rank, which the general fold leaves alone).
	for name, tc := range map[string]struct {
		twin, twinName, first, last, dob, dod, bare string
		id                                          int
	}{
		"courtesy title": {"sir-frederick-treves", "Sir Frederick Treves", "Frederick", "Treves", "1853", "1923", "frederick-treves", 14047},
		"military rank":  {"colonel-joseph-h-alexander", "Colonel Joseph H. Alexander", "Joseph H.", "Alexander", "1938", "2014", "joseph-h-alexander", 15809},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{personAddr(tc.twin): personFiles(t, tc.twin, tc.twinName, nil)}
			retailWork(t, files, "a-retail-book", "A Retail Book", tc.twin, "")
			seedTree(t, dataDir, files)
			snap := writeSnapshot(t, byAuthors(t, 99001, "Elephant Man and Other Reminiscences", lvDated(tc.id, tc.first, tc.last, tc.dob, tc.dod)))
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if got := workAuthorsOf(t, dataDir, "elephant-man-and-other-reminiscences"); !reflect.DeepEqual(got, []string{tc.twin}) {
				t.Fatalf("authors = %v\nnotes: %v", got, sum.Notes)
			}
			if entryExists(t, dataDir, personAddr(tc.bare)) {
				t.Errorf("%s was minted", tc.bare)
			}
			if p := personOf(t, dataDir, tc.twin); p.AuthorID != tc.id || p.DeathYear == 0 {
				t.Errorf("%s = %+v, want keyed with the years", tc.twin, p)
			}
			mustValidate(t, dataDir)
			rerunIsANoOp(t, snap, dataDir)
		})
	}
}

func TestLibrivoxTitledTwinWithOtherYearsIsSomeoneElse(t *testing.T) {
	// The Platts the other way round: a bare William Platt (1867-1943) never
	// lands on a catalogued Mrs. William Platt (d. 1942, another id).
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("mrs-william-platt"): personFiles(t, "mrs-william-platt", "Mrs. William Platt",
			map[string]any{"librivox_author_id": 14096, "death_year": 1942}),
	})
	runLibrivox(t, writeSnapshot(t, byAuthors(t, 99002, "Another Book", lvDated(13717, "William", "Platt", "1867", "1943"))), dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "another-book"); !reflect.DeepEqual(got, []string{"william-platt"}) {
		t.Fatalf("authors = %v", got)
	}
}

func TestLibrivoxTitledAuthorIsKeptOffANarrator(t *testing.T) {
	// 16463 "Amid the High Hills": "Sir Hugh Fraser" (1903-1966) folded onto
	// hugh-fraser, the actor who narrates Poirot.
	dataDir := t.TempDir()
	files := map[string]string{personAddr("hugh-fraser"): personFiles(t, "hugh-fraser", "Hugh Fraser", nil)}
	narratedWork(t, files, "curtain", "Curtain", "hugh-fraser")
	seedTree(t, dataDir, files)
	snap := writeSnapshot(t, byAuthors(t, 16463, "Amid the High Hills", lvDated(15970, "Sir Hugh", "Fraser", "1903", "1966")))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "amid-the-high-hills"); !reflect.DeepEqual(got, []string{"sir-hugh-fraser"}) {
		t.Fatalf("authors = %v\nhonorific: %v", got, sum.HonorificMerges)
	}
	if p := personOf(t, dataDir, "hugh-fraser"); p.AuthorID != 0 || p.DeathYear != 0 {
		t.Errorf("hugh-fraser was stamped: %+v", p)
	}
	if p := personOf(t, dataDir, "sir-hugh-fraser"); p.AuthorID != 15970 || p.BirthYear != 1903 {
		t.Errorf("sir-hugh-fraser = %+v", p)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxGenuineCourtesyTitleStillFolds(t *testing.T) {
	// The fold's own case: "Sir Arthur Conan Doyle" is the catalogued
	// arthur-conan-doyle (no id, no years) when they share a title, or when
	// his works are dated within his life.
	for name, tc := range map[string]struct{ title, retailTitle, firstPublished string }{
		"title in common": {"The Lost World", "The Lost World", ""},
		"dated work":      {"The Poison Belt", "The Land of Mist", "1926"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{personAddr("arthur-conan-doyle"): personFiles(t, "arthur-conan-doyle", "Arthur Conan Doyle", nil)}
			retailWork(t, files, "retail", tc.retailTitle, "arthur-conan-doyle", tc.firstPublished)
			seedTree(t, dataDir, files)
			sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99003, tc.title, lvDated(69, "Sir Arthur Conan", "Doyle", "1859", "1930"))), dataDir, allLibrivox)
			if entryExists(t, dataDir, personAddr("sir-arthur-conan-doyle")) {
				t.Fatalf("sir-arthur-conan-doyle was minted\nnotes: %v", sum.Notes)
			}
			if p := personOf(t, dataDir, "arthur-conan-doyle"); p.AuthorID != 69 {
				t.Errorf("arthur-conan-doyle = %+v", p)
			}
		})
	}
}

// Rule 2: same-name authors.

func TestLibrivoxSameNameAuthorIsMintedDated(t *testing.T) {
	// The catalogued record is someone else, and LibriVox states years: the id
	// is a person of its own at the dated name the tranches minted by hand.
	for name, tc := range map[string]struct {
		seed               func(t *testing.T, files map[string]string)
		author             LibrivoxPerson
		slug, wantName     string
		plain              string
		plainKeepsAuthorID int
	}{
		// 16764: John Tyndall (1820-1893) onto a retail narrator.
		"narrator only": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("john-tyndall")] = personFiles(t, "john-tyndall", "John Tyndall", nil)
				narratedWork(t, files, "grow-indoors", "How to Grow Indoors", "john-tyndall")
			},
			author: lvDated(16171, "John", "Tyndall", "1820", "1893"),
			slug:   "john-tyndall-1820-1893", wantName: "John Tyndall (1820-1893)", plain: "john-tyndall",
		},
		// A record whose works are first published long after the death.
		"published long after": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("james-grant")] = personFiles(t, "james-grant", "James Grant", nil)
				retailWork(t, files, "boombustology", "Boombustology", "james-grant", "2011")
			},
			author: lvDated(16128, "James", "Grant", "1822", "1887"),
			slug:   "james-grant-1822-1887", wantName: "James Grant (1822-1887)", plain: "james-grant",
		},
		// 9913: Richard Wilson (fl. 1916) onto richard-wilson (1920-1987, 555).
		"another author id, flourished": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("richard-wilson")] = personFiles(t, "richard-wilson", "Richard Wilson",
					map[string]any{"librivox_author_id": 555, "birth_year": 1920, "death_year": 1987})
			},
			author: lvDated(11394, "Richard", "Wilson", "", "fl.1916"),
			slug:   "richard-wilson-fl-1916", wantName: "Richard Wilson (fl. 1916)", plain: "richard-wilson", plainKeepsAuthorID: 555,
		},
		// 14605: John Brown (1715-1766) onto john-brown (1830-1922, 12220).
		"another author id": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("john-brown")] = personFiles(t, "john-brown", "John Brown",
					map[string]any{"librivox_author_id": 12220, "birth_year": 1830, "death_year": 1922})
			},
			author: lvDated(15534, "John", "Brown", "1715", "1766"),
			slug:   "john-brown-1715-1766", wantName: "John Brown (1715-1766)", plain: "john-brown", plainKeepsAuthorID: 12220,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{}
			tc.seed(t, files)
			seedTree(t, dataDir, files)
			before := personOf(t, dataDir, tc.plain)
			snap := writeSnapshot(t, byAuthors(t, 99010, "A Public Domain Text", tc.author))
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if got := workAuthorsOf(t, dataDir, "a-public-domain-text"); !reflect.DeepEqual(got, []string{tc.slug}) {
				t.Fatalf("authors = %v\nnotes: %v\nworklist: %v", got, sum.Notes, sum.LibrivoxWorklist)
			}
			p := personOf(t, dataDir, tc.slug)
			if p.Name != tc.wantName || p.AuthorID != int(tc.author.ID) {
				t.Errorf("%s = %+v", tc.slug, p)
			}
			if got := personOf(t, dataDir, tc.plain); got != before || got.AuthorID != tc.plainKeepsAuthorID {
				t.Errorf("%s changed: %+v -> %+v", tc.plain, before, got)
			}
			if !model.PersonIDMatches(tc.slug, p.Name, 0) {
				t.Errorf("%s does not match its name %q", tc.slug, p.Name)
			}
			mustValidate(t, dataDir)
			rerunIsANoOp(t, snap, dataDir)

			// A later project of the id finds the dated record by id.
			more := writeSnapshot(t, byAuthors(t, 99011, "Another Public Domain Text", tc.author))
			again := runLibrivox(t, more, dataDir, allLibrivox)
			if got := workAuthorsOf(t, dataDir, "another-public-domain-text"); !reflect.DeepEqual(got, []string{tc.slug}) || again.NewPeople != 0 {
				t.Errorf("later project authors = %v, new people %d", got, again.NewPeople)
			}
		})
	}
}

func TestLibrivoxSameNameAuthorGoesToTheWorklist(t *testing.T) {
	// Not stamped, not minted: LibriVox states no year to mint by (Ralph Scott,
	// 17170, onto a retail narrator), or the catalogued author's works are
	// dated only by their recordings' release dates, which date a modern book
	// and a modern edition of an old one alike (James Grant, Bagehot).
	for name, tc := range map[string]struct {
		seed       func(t *testing.T, files map[string]string)
		author     LibrivoxPerson
		plain      string
		candidates []LibrivoxCandidate
	}{
		"no years": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("ralph-scott")] = personFiles(t, "ralph-scott", "Ralph Scott", nil)
				narratedWork(t, files, "the-hidden-road", "The Hidden Road", "ralph-scott")
			},
			author: lvDated(16428, "Ralph", "Scott", "", ""), plain: "ralph-scott", candidates: []LibrivoxCandidate{},
		},
		"dated only by recordings": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("james-grant")] = personFiles(t, "james-grant", "James Grant", nil)
				retailWork(t, files, "bagehot", "Bagehot", "james-grant", "")
			},
			author: lvDated(16128, "James", "Grant", "1822", "1887"), plain: "james-grant",
			candidates: []LibrivoxCandidate{{Work: "bagehot", Title: "Bagehot"}},
		},
		"another id, no years": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("andy-adams")] = personFiles(t, "andy-adams", "Andy Adams", map[string]any{"librivox_author_id": 940})
				retailWork(t, files, "trail-drive", "Trail Drive", "andy-adams", "")
			},
			author: lvDated(12675, "Andy", "Adams", "", ""), plain: "andy-adams",
			candidates: []LibrivoxCandidate{{Work: "trail-drive", Title: "Trail Drive"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{}
			tc.seed(t, files)
			seedTree(t, dataDir, files)
			before := testpack.Snapshot(t, dataDir)
			sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 17170, "Soldier's Diary", tc.author)), dataDir, allLibrivox)
			want := []LibrivoxWorklistEntry{{
				Project: 17170, Title: "Soldier's Diary", Authors: []string{tc.author.name()},
				Reason: LibrivoxSameNameAuthor, Candidates: tc.candidates,
			}}
			if !reflect.DeepEqual(sum.LibrivoxWorklist, want) || sum.NewWorks+sum.NewRecordings+sum.NewPeople != 0 {
				t.Fatalf("worklist = %+v, works/recordings/people = %d/%d/%d", sum.LibrivoxWorklist, sum.NewWorks, sum.NewRecordings, sum.NewPeople)
			}
			if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
				t.Error("a worklisted row changed the tree")
			}
			if !strings.Contains(sum.LibrivoxWorklist[0].String(), "[same-name-author]") {
				t.Errorf("line = %s", sum.LibrivoxWorklist[0].String())
			}
		})
	}
}

func TestLibrivoxGenuineSameNameAuthorStillAttaches(t *testing.T) {
	// The match rule 2 must keep: nothing says the catalogued record is
	// someone else, so it is credited and keyed as before.
	for name, tc := range map[string]struct {
		seed   func(t *testing.T, files map[string]string)
		title  string
		author LibrivoxPerson
		plain  string
	}{
		"title in common": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("john-tyndall")] = personFiles(t, "john-tyndall", "John Tyndall", nil)
				retailWork(t, files, "fragments-of-science", "Fragments of Science", "john-tyndall", "")
			},
			title: "Fragments of Science", author: lvDated(16171, "John", "Tyndall", "1820", "1893"), plain: "john-tyndall",
		},
		"published in life": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("john-tyndall")] = personFiles(t, "john-tyndall", "John Tyndall", nil)
				retailWork(t, files, "hours-of-exercise", "Hours of Exercise in the Alps", "john-tyndall", "1871")
			},
			title: "Glaciers of the Alps", author: lvDated(16171, "John", "Tyndall", "1820", "1893"), plain: "john-tyndall",
		},
		"another id, the same years": {
			// Margaret Steele Anderson under LibriVox ids 12383 and 17348.
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("margaret-steele-anderson")] = personFiles(t, "margaret-steele-anderson", "Margaret Steele Anderson",
					map[string]any{"librivox_author_id": 12383, "birth_year": 1867, "death_year": 1921})
			},
			title: "The Flame in the Wind", author: lvDated(17348, "Margaret Steele", "Anderson", "1867", "1921"), plain: "margaret-steele-anderson",
		},
		"no years, author records": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("r-p-jones")] = personFiles(t, "r-p-jones", "R. P. Jones", nil)
				retailWork(t, files, "chrysalis", "Chrysalis", "r-p-jones", "")
			},
			title: "Nine Lives", author: lvDated(18564, "R. P.", "Jones", "", ""), plain: "r-p-jones",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{}
			tc.seed(t, files)
			seedTree(t, dataDir, files)
			sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99020, tc.title, tc.author)), dataDir, allLibrivox)
			if len(sum.LibrivoxWorklist) != 0 || sum.NewRecordings != 1 {
				t.Fatalf("worklist = %v, recordings %d\nnotes: %v", sum.LibrivoxWorklist, sum.NewRecordings, sum.Notes)
			}
			if got := workAuthorsOf(t, dataDir, Slugify(tc.title)); !reflect.DeepEqual(got, []string{tc.plain}) {
				t.Errorf("authors = %v, want [%s]\nnotes: %v", got, tc.plain, sum.Notes)
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestLibrivoxTwoAuthorIDsMeetingOneFreeNameInARun(t *testing.T) {
	// Tranche 8's John Browns, with no catalogued john-brown: the first id
	// keeps the plain name, the second (other years) is dated; one with no
	// years beside them is not told apart.
	snap := writeSnapshot(t,
		byAuthors(t, 14605, "Estimate of the Manners", lvDated(15534, "John", "Brown", "1715", "1766")),
		byAuthors(t, 15082, "Rab and His Friends", lvDated(10611, "John", "Brown", "1810", "1882")),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "estimate-of-the-manners"); !reflect.DeepEqual(got, []string{"john-brown"}) {
		t.Errorf("first = %v", got)
	}
	if got := workAuthorsOf(t, dataDir, "rab-and-his-friends"); !reflect.DeepEqual(got, []string{"john-brown-1810-1882"}) {
		t.Errorf("second = %v\nnotes: %v", got, sum.Notes)
	}
	if p := personOf(t, dataDir, "john-brown"); p.AuthorID != 15534 || p.BirthYear != 1715 {
		t.Errorf("john-brown = %+v", p)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxFlourishedYear(t *testing.T) {
	for _, tc := range []struct {
		dob, dod string
		want     lifeYears
	}{
		{"", "fl.1916", lifeYears{fl: 1916}},
		{"", "fl. 1925", lifeYears{fl: 1925}},
		{"fl.1844", "", lifeYears{fl: 1844}},
		{"1850", "fl.1900", lifeYears{birth: 1850}},
		{"", "post-1919", lifeYears{}},
	} {
		if got := lifeYearsOf(LibrivoxPerson{DOB: lvText(tc.dob), DOD: lvText(tc.dod)}); got != tc.want {
			t.Errorf("lifeYearsOf(%q, %q) = %+v, want %+v", tc.dob, tc.dod, got, tc.want)
		}
	}
	for _, tc := range []struct {
		life lifeYears
		want string
	}{
		{lifeYears{birth: 1822, death: 1887}, "James Grant (1822-1887)"},
		{lifeYears{death: 1942}, "James Grant (d. 1942)"},
		{lifeYears{birth: 1850}, "James Grant (b. 1850)"},
		{lifeYears{fl: 1916}, "James Grant (fl. 1916)"},
		{lifeYears{birth: -43, death: 17}, "James Grant (43 BC-17)"},
		{lifeYears{}, ""},
	} {
		if got := datedName("James Grant", tc.life); got != tc.want {
			t.Errorf("datedName(%+v) = %q, want %q", tc.life, got, tc.want)
		}
	}
}

// Rule 3: readers on authors.

func TestLibrivoxReaderIsSplitOffAnAuthorRecord(t *testing.T) {
	// 20320: reader 19341 "Adam Smith" would have narrated as Adam Smith
	// (1723-1790, LibriVox author 395); 19883: reader 19252 "James Thomson"
	// as the poet who died in 1882 (years only, no author id).
	for name, fields := range map[string]map[string]any{
		"author id":  {"librivox_author_id": 395, "birth_year": 1723, "death_year": 1790},
		"died early": {"death_year": 1790},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedTree(t, dataDir, map[string]string{personAddr("adam-smith"): personFiles(t, "adam-smith", "Adam Smith", fields)})
			snap := writeSnapshot(t, readProject(t, 20320, LibrivoxReader{ReaderID: 19341, DisplayName: "Adam Smith"}))
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if got := lvNarrators(t, dataDir, "metamorphosis", "adam-smith-librivox-19341"); !reflect.DeepEqual(got, []string{"adam-smith-librivox-19341"}) {
				t.Fatalf("narrators = %v\nwarnings: %v", got, sum.Warnings)
			}
			if p := personOf(t, dataDir, "adam-smith"); p.ReaderID != 0 {
				t.Errorf("adam-smith carries reader %d", p.ReaderID)
			}
			if p := personOf(t, dataDir, "adam-smith-librivox-19341"); p.ReaderID != 19341 || p.Name != "Adam Smith" {
				t.Errorf("split reader = %+v", p)
			}
			if sum.LibrivoxReadersSplit != 1 {
				t.Errorf("LibrivoxReadersSplit = %d, want 1", sum.LibrivoxReadersSplit)
			}
			mustValidate(t, dataDir)
			rerunIsANoOp(t, snap, dataDir)
		})
	}
}

func TestLibrivoxReaderStillClaimsALivingOrUndatedRecord(t *testing.T) {
	// A retail record with no author id and no years, or one who died after
	// LibriVox began, is not an author record: the reader claims it as before.
	for name, fields := range map[string]map[string]any{
		"no id, no years": nil,
		"died since 2005": {"birth_year": 1950, "death_year": 2020},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedTree(t, dataDir, map[string]string{personAddr("adam-smith"): personFiles(t, "adam-smith", "Adam Smith", fields)})
			sum := runLibrivox(t, writeSnapshot(t, readProject(t, 20320, LibrivoxReader{ReaderID: 19341, DisplayName: "Adam Smith"})), dataDir, allLibrivox)
			if p := personOf(t, dataDir, "adam-smith"); p.ReaderID != 19341 || sum.LibrivoxReadersSplit != 0 {
				t.Errorf("adam-smith = %+v, split %d", p, sum.LibrivoxReadersSplit)
			}
		})
	}
}
