package importer

// The three person rules of librivoxpeople.go (meta#2778), each from the real
// cases LibriVox tranches 7 to 11 fixed by hand, beside the genuine match each
// rule must keep.

import (
	"encoding/json"
	"reflect"
	"slices"
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
	// AuthorAliases is read back to show no run writes it and every run keeps it.
	AuthorAliases []int `json:"librivox_author_aliases"`
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
	want := []LibrivoxAuthorPair{{
		Titled: LibrivoxPairAuthor{AuthorID: 14096, Name: "Mrs. William Platt", Person: "mrs-william-platt", Project: 13419},
		Bare:   LibrivoxPairAuthor{AuthorID: 13717, Name: "William Platt", Person: "william-platt", Project: 13419},
	}}
	if !reflect.DeepEqual(sum.LibrivoxAuthorPairs, want) {
		t.Errorf("pairs = %+v, want %+v", sum.LibrivoxAuthorPairs, want)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxBatchAuthorIDsOneTitleApartNeverFold(t *testing.T) {
	// Two author ids of one batch a courtesy title apart are two people even
	// with the same years (meta#2799, the owner's rule): LibriVox's own
	// duplicate (Livingston 1456 / 15071) and two people (the Platts) look
	// alike, so the run keeps both records and lists the pair for a person.
	snap := writeSnapshot(t,
		byAuthors(t, 4714, "Florence Stanley", lvDated(1456, "Mrs. C. M.", "Livingston", "1832", "1924")),
		byAuthors(t, 15071, "The Old Homestead", lvDated(15090, "C. M.", "Livingston", "1832", "1924")))
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "florence-stanley"); !reflect.DeepEqual(got, []string{"mrs-c-m-livingston"}) {
		t.Fatalf("florence-stanley authors = %v", got)
	}
	if got := workAuthorsOf(t, dataDir, "the-old-homestead"); !reflect.DeepEqual(got, []string{"c-m-livingston"}) {
		t.Fatalf("the-old-homestead authors = %v", got)
	}
	if len(sum.LibrivoxAuthorPairs) != 1 || sum.LibrivoxAuthorPairs[0].String() !=
		`1456 "Mrs. C. M. Livingston" -> mrs-c-m-livingston (project 4714); 15090 "C. M. Livingston" -> c-m-livingston (project 15071)` {
		t.Errorf("pairs = %v", sum.LibrivoxAuthorPairs)
	}
	if len(sum.HonorificMerges) != 0 || len(sum.LibrivoxWorklist) != 0 {
		t.Errorf("honorific merges %v, worklist %v, want none", sum.HonorificMerges, sum.LibrivoxWorklist)
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
		split              bool
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
		// No year to date the name by (meta#2778): the author-id form.
		// 17170: Ralph Scott (16428) onto a retail narrator.
		"no years, narrator only": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("ralph-scott")] = personFiles(t, "ralph-scott", "Ralph Scott", nil)
				narratedWork(t, files, "the-hidden-road", "The Hidden Road", "ralph-scott")
			},
			author: lvDated(16428, "Ralph", "Scott", "", ""),
			slug:   "ralph-scott-librivox-author-16428", wantName: "Ralph Scott", plain: "ralph-scott", split: true,
		},
		// 12675: Andy Adams beside andy-adams (940).
		"another author id, no years": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("andy-adams")] = personFiles(t, "andy-adams", "Andy Adams", map[string]any{"librivox_author_id": 940})
				retailWork(t, files, "trail-drive", "Trail Drive", "andy-adams", "")
			},
			author: lvDated(12675, "Andy", "Adams", "", ""),
			slug:   "andy-adams-librivox-author-12675", wantName: "Andy Adams", plain: "andy-adams", plainKeepsAuthorID: 940, split: true,
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
			if got := personOf(t, dataDir, tc.plain); !reflect.DeepEqual(got, before) || got.AuthorID != tc.plainKeepsAuthorID {
				t.Errorf("%s changed: %+v -> %+v", tc.plain, before, got)
			}
			if !model.PersonIDMatches(tc.slug, p.Name, 0, p.AuthorID) {
				t.Errorf("%s does not match its name %q", tc.slug, p.Name)
			}
			var splits []LibrivoxAuthorSplit
			if tc.split {
				splits = []LibrivoxAuthorSplit{{Author: LibrivoxPairAuthor{AuthorID: int(tc.author.ID), Name: tc.wantName, Person: tc.slug, Project: 99010}, Against: tc.plain}}
			}
			if !reflect.DeepEqual(sum.LibrivoxAuthorSplits, splits) {
				t.Errorf("splits = %+v, want %+v", sum.LibrivoxAuthorSplits, splits)
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
	// Not stamped, not minted: the catalogued author's works are dated only by
	// their recordings' release dates, which date a modern book and a modern
	// edition of an old one alike (James Grant, Bagehot), or the name is a
	// collective statement's, which no author id splits.
	for name, tc := range map[string]struct {
		seed       func(t *testing.T, files map[string]string)
		author     LibrivoxPerson
		plain      string
		candidates []LibrivoxCandidate
	}{
		"dated only by recordings": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("james-grant")] = personFiles(t, "james-grant", "James Grant", nil)
				retailWork(t, files, "bagehot", "Bagehot", "james-grant", "")
			},
			author: lvDated(16128, "James", "Grant", "1822", "1887"), plain: "james-grant",
			candidates: []LibrivoxCandidate{{Work: "bagehot", Title: "Bagehot"}},
		},
		// A collective statement is never split by an id: two LibriVox
		// "Anonymous" records stay the one collective's question.
		"a collective, another id": {
			seed: func(t *testing.T, files map[string]string) {
				files[personAddr("anonymous")] = personFiles(t, "anonymous", "Anonymous", map[string]any{"librivox_author_id": 19})
				retailWork(t, files, "beowulf", "Beowulf", "anonymous", "")
			},
			author: lvDated(23001, "", "Anonymous", "", ""), plain: "anonymous",
			candidates: []LibrivoxCandidate{{Work: "beowulf", Title: "Beowulf"}},
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

// Review regressions: each case below reached the wrong person before.

func TestLibrivoxPersonRulesEdgeCases(t *testing.T) {
	t.Run("flourished year outside a catalogued life", func(t *testing.T) {
		// Richard Wilson (fl. 1916) is not the catalogued Richard Wilson born
		// in 1920, though that record carries no author id.
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{
			personAddr("richard-wilson"): personFiles(t, "richard-wilson", "Richard Wilson", map[string]any{"birth_year": 1920, "death_year": 1987}),
		})
		runLibrivox(t, writeSnapshot(t, byAuthors(t, 99060, "Old Text", lvDated(11394, "Richard", "Wilson", "", "fl.1916"))), dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "old-text"); !reflect.DeepEqual(got, []string{"richard-wilson-fl-1916"}) {
			t.Errorf("authors = %v", got)
		}
		if p := personOf(t, dataDir, "richard-wilson"); p.AuthorID != 0 {
			t.Errorf("richard-wilson was stamped: %+v", p)
		}
	})
	t.Run("titled name meeting a titled someone else", func(t *testing.T) {
		// Rule 2 reads a titled credit's own name too.
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{
			personAddr("mrs-henry-wood"): personFiles(t, "mrs-henry-wood", "Mrs. Henry Wood",
				map[string]any{"librivox_author_id": 500, "birth_year": 1814, "death_year": 1887}),
		})
		runLibrivox(t, writeSnapshot(t, byAuthors(t, 99070, "Modern Text", lvDated(600, "Mrs. Henry", "Wood", "1900", "1950"))), dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "modern-text"); !reflect.DeepEqual(got, []string{"mrs-henry-wood-1900-1950"}) {
			t.Errorf("authors = %v", got)
		}
		mustValidate(t, dataDir)
	})
	t.Run("bare name behind a titled narrator", func(t *testing.T) {
		// Rule 1c never lands an author on a titled twin who only narrates.
		dataDir := t.TempDir()
		files := map[string]string{personAddr("sir-derek-jacobi"): personFiles(t, "sir-derek-jacobi", "Sir Derek Jacobi", nil)}
		narratedWork(t, files, "curtain", "Curtain", "sir-derek-jacobi")
		seedTree(t, dataDir, files)
		runLibrivox(t, writeSnapshot(t, byAuthors(t, 99080, "Old Poems", lvDated(777, "Derek", "Jacobi", "1801", "1870"))), dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "old-poems"); !reflect.DeepEqual(got, []string{"derek-jacobi"}) {
			t.Errorf("authors = %v", got)
		}
		if p := personOf(t, dataDir, "sir-derek-jacobi"); p.AuthorID != 0 || p.DeathYear != 0 {
			t.Errorf("sir-derek-jacobi was stamped: %+v", p)
		}
	})
	t.Run("undated translator who is a catalogued reader", func(t *testing.T) {
		// LibriVox's undated translators are its own volunteers: one who
		// already reads for it is not a narrator someone else.
		dataDir := t.TempDir()
		files := map[string]string{personAddr("jane-doe"): personFiles(t, "jane-doe", "Jane Doe", map[string]any{"librivox_reader_id": 123})}
		narratedWork(t, files, "curtain", "Curtain", "jane-doe")
		seedTree(t, dataDir, files)
		sum := runLibrivox(t, writeSnapshot(t, byAuthor(t, 99090, "Old Foreign Book", "English",
			lvDated(800, "Hans", "Muller", "1801", "1870"), lvDated(9000, "Jane", "Doe", "", ""))), dataDir, allLibrivox)
		if len(sum.LibrivoxWorklist) != 0 || sum.NewRecordings != 1 {
			t.Fatalf("worklist = %v, recordings %d", sum.LibrivoxWorklist, sum.NewRecordings)
		}
		if p := personOf(t, dataDir, "jane-doe"); p.AuthorID != 9000 || p.ReaderID != 123 {
			t.Errorf("jane-doe = %+v", p)
		}
	})
}

func TestLibrivoxTitledAuthorUnsureGoesToTheWorklist(t *testing.T) {
	// Sir Arthur Conan Doyle beside a catalogued Arthur Conan Doyle whose only
	// work is dated by its recording's release date: a person decides, rather
	// than the run minting sir-arthur-conan-doyle beside him.
	dataDir := t.TempDir()
	files := map[string]string{personAddr("arthur-conan-doyle"): personFiles(t, "arthur-conan-doyle", "Arthur Conan Doyle", nil)}
	retailWork(t, files, "retail", "The Land of Mist", "arthur-conan-doyle", "")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99003, "The Poison Belt", lvDated(69, "Sir Arthur Conan", "Doyle", "1859", "1930"))), dataDir, allLibrivox)
	want := []LibrivoxWorklistEntry{{
		Project: 99003, Title: "The Poison Belt", Authors: []string{"Sir Arthur Conan Doyle"},
		Reason: LibrivoxSameNameAuthor, Candidates: []LibrivoxCandidate{{Work: "retail", Title: "The Land of Mist"}},
	}}
	if !reflect.DeepEqual(sum.LibrivoxWorklist, want) || sum.NewPeople != 0 {
		t.Fatalf("worklist = %+v, new people %d", sum.LibrivoxWorklist, sum.NewPeople)
	}
}

func TestLibrivoxSameNameSkipCountsNoAttach(t *testing.T) {
	// A worklisted row whose title is a spelling variant of the catalogued
	// person's work is not also reported as attached to it.
	dataDir := t.TempDir()
	files := map[string]string{personAddr("andy-adams"): personFiles(t, "andy-adams", "Andy Adams", map[string]any{"librivox_author_id": 940})}
	retailWork(t, files, "the-trail-drive", "The Trail Drive", "andy-adams", "")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 17170, "Trail Drive", lvDated(12675, "Andy", "Adams", "", ""))), dataDir, allLibrivox)
	if len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxVariantsAttached != 0 || sum.LibrivoxAuthorVariants != 0 {
		t.Errorf("worklist = %v, variants attached %d, author variants %d", sum.LibrivoxWorklist, sum.LibrivoxVariantsAttached, sum.LibrivoxAuthorVariants)
	}
}

func TestLibrivoxReaderMetBeforeADeadAuthorOfTheRun(t *testing.T) {
	// Rule 3 whatever the row order: a reader called Adam Smith met before
	// the run's Adam Smith (1723-1790) is still a person of their own.
	dataDir := t.TempDir()
	snap := writeSnapshot(t,
		readProject(t, 20320, LibrivoxReader{ReaderID: 19341, DisplayName: "Adam Smith"}),
		byAuthors(t, 99050, "Wealth of Nations", lvDated(395, "Adam", "Smith", "1723", "1790")),
	)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if p := personOf(t, dataDir, "adam-smith"); p.ReaderID != 0 || p.AuthorID != 395 {
		t.Errorf("adam-smith = %+v", p)
	}
	if sum.LibrivoxReadersSplit != 1 {
		t.Errorf("LibrivoxReadersSplit = %d, want 1", sum.LibrivoxReadersSplit)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxGenericTitleIsNoTitleInCommon(t *testing.T) {
	// Two poets may each publish "Poems": a generic title shared with the
	// catalogued twin does not outweigh dates that say they are two people.
	for name, tc := range map[string]struct {
		published string
		dated     bool
	}{
		"first published long after": {"1998", true},
		"dated by release dates":     {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{}
			files[personAddr("james-grant")] = personFiles(t, "james-grant", "James Grant", nil)
			retailWork(t, files, "selected-poems", "Selected Poems", "james-grant", tc.published)
			seedTree(t, dataDir, files)
			snap := writeSnapshot(t, byAuthors(t, 99020, "Selected Poems (1880)", lvDated(16128, "James", "Grant", "1822", "1887")))
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if p := personOf(t, dataDir, "james-grant"); p.AuthorID != 0 {
				t.Fatalf("james-grant stamped: %+v", p)
			}
			if tc.dated {
				if !entryExists(t, dataDir, personAddr("james-grant-1822-1887")) {
					t.Errorf("no dated person\nnotes: %v\nworklist: %v", sum.Notes, sum.LibrivoxWorklist)
				}
				return
			}
			if len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxWorklist[0].Reason != LibrivoxSameNameAuthor {
				t.Errorf("worklist = %+v", sum.LibrivoxWorklist)
			}
		})
	}
}

func TestLibrivoxGenericTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"Poems":                       true,
		"Selected Poems":              true,
		"Short Stories":               true,
		"The Essays and Other Pieces": true,
		"Letters, Volume 2":           true,
		"Poems (1915)":                true,
		"Collected Works [Johnston]":  true,
		"Poems of Conformity":         false,
		"Letters from Egypt":          false,
		"Fragments of Science":        false,
		"1601":                        false,
		"":                            false,
	} {
		if got := librivoxGenericTitle(title); got != want {
			t.Errorf("librivoxGenericTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestLibrivoxSecondUndatedIDInARunIsAPersonOfItsOwn(t *testing.T) {
	// Two undated author ids of one free name: the first keeps the plain
	// name, the second is filed at its author-id form (rule 4, meta#2778) and
	// listed beside the first for a person to merge or confirm - in one run
	// exactly as in two, never credited onto the first's record.
	first := byAuthors(t, 99030, "A Soldier's Diary", LibrivoxPerson{ID: lvInt(17170), FirstName: "Ralph", LastName: "Scott"})
	second := byAuthors(t, 99031, "Letters Home", LibrivoxPerson{ID: lvInt(17999), FirstName: "Ralph", LastName: "Scott"})
	const minted = "ralph-scott-librivox-author-17999"
	wantSplits := []LibrivoxAuthorSplit{{Author: LibrivoxPairAuthor{AuthorID: 17999, Name: "Ralph Scott", Person: minted, Project: 99031}, Against: "ralph-scott"}}

	oneRun := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, first, second), oneRun, allLibrivox)
	if len(sum.LibrivoxWorklist) != 0 || !reflect.DeepEqual(sum.LibrivoxAuthorSplits, wantSplits) {
		t.Fatalf("worklist %v, splits %+v\nnotes: %v", sum.LibrivoxWorklist, sum.LibrivoxAuthorSplits, sum.Notes)
	}
	if p := personOf(t, oneRun, "ralph-scott"); p.AuthorID != 17170 {
		t.Errorf("ralph-scott = %+v", p)
	}
	if got := workAuthorsOf(t, oneRun, "letters-home"); !reflect.DeepEqual(got, []string{minted}) {
		t.Errorf("letters-home authors = %v", got)
	}
	if p := personOf(t, oneRun, minted); p.AuthorID != 17999 || p.Name != "Ralph Scott" {
		t.Errorf("%s = %+v", minted, p)
	}
	mustValidate(t, oneRun)

	twoRuns := t.TempDir()
	runLibrivox(t, writeSnapshot(t, first), twoRuns, allLibrivox)
	later := runLibrivox(t, writeSnapshot(t, second), twoRuns, allLibrivox)
	if !reflect.DeepEqual(later.LibrivoxAuthorSplits, wantSplits) {
		t.Errorf("the later run's splits = %+v, want %+v", later.LibrivoxAuthorSplits, wantSplits)
	}
	if !reflect.DeepEqual(testpack.Snapshot(t, oneRun), testpack.Snapshot(t, twoRuns)) {
		t.Error("one run and two runs left different trees")
	}
	rerunIsANoOp(t, writeSnapshot(t, first, second), oneRun)

	// The same birth and death years under two ids are one person in a run.
	same := t.TempDir()
	sum = runLibrivox(t, writeSnapshot(t,
		byAuthors(t, 99032, "The Flame in the Wind", lvDated(12383, "Margaret Steele", "Anderson", "1867", "1921")),
		byAuthors(t, 99033, "The Heart of Life", lvDated(17348, "Margaret Steele", "Anderson", "1867", "1921")),
	), same, allLibrivox)
	if len(sum.LibrivoxWorklist) != 0 || sum.NewRecordings != 2 {
		t.Fatalf("worklist = %+v, recordings = %d", sum.LibrivoxWorklist, sum.NewRecordings)
	}
	for _, w := range []string{"the-flame-in-the-wind", "the-heart-of-life"} {
		if got := workAuthorsOf(t, same, w); !reflect.DeepEqual(got, []string{"margaret-steele-anderson"}) {
			t.Errorf("%s authors = %v", w, got)
		}
	}
	mustValidate(t, same)
}

func TestLibrivoxSecondIDInARunMeetsTheCataloguedYears(t *testing.T) {
	// The first id lands on a catalogued dated person, so a later run judges
	// the second id by that person's years, not the first id's (none): one run
	// must credit the second there too, not mint a dated twin of the same years.
	seed := func() string {
		dataDir := t.TempDir()
		files := map[string]string{}
		files[personAddr("john-smith")] = personFiles(t, "john-smith", "John Smith", map[string]any{"birth_year": 1800, "death_year": 1870})
		retailWork(t, files, "old-sermons-of-the-moor", "Old Sermons of the Moor", "john-smith", "1850")
		seedTree(t, dataDir, files)
		return dataDir
	}
	first := byAuthors(t, 99040, "Alpha Tales", LibrivoxPerson{ID: lvInt(5001), FirstName: "John", LastName: "Smith"})
	second := byAuthors(t, 99041, "Beta Tales", lvDated(5002, "John", "Smith", "1800", "1870"))

	oneRun := seed()
	runLibrivox(t, writeSnapshot(t, first, second), oneRun, allLibrivox)
	if got := workAuthorsOf(t, oneRun, "beta-tales"); !reflect.DeepEqual(got, []string{"john-smith"}) {
		t.Errorf("beta-tales authors = %v", got)
	}
	twoRuns := seed()
	runLibrivox(t, writeSnapshot(t, first), twoRuns, allLibrivox)
	runLibrivox(t, writeSnapshot(t, second), twoRuns, allLibrivox)
	if !reflect.DeepEqual(testpack.Snapshot(t, oneRun), testpack.Snapshot(t, twoRuns)) {
		t.Error("one run and two runs left different trees")
	}
}

// The person a credit names is decided once, by LibriVox id, after the
// batch's initials step (meta#2799).

// withReader is pr read by one reader throughout.
func withReader(pr LibrivoxProject, id int, name string) LibrivoxProject {
	pr.Sections = append([]LibrivoxSection(nil), pr.Sections...)
	for i := range pr.Sections {
		pr.Sections[i].Readers = []LibrivoxReader{{ReaderID: lvInt(id), DisplayName: name}}
	}
	return pr
}

// treeWithoutSources is the data tree at dir with every record's sources
// removed: a record's provenance lists the projects that wrote it in the order
// the run met them, which is the one thing a row order may change.
func treeWithoutSources(t *testing.T, dir string) map[string]any {
	t.Helper()
	var strip func(v any) any
	strip = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "sources")
			for k, e := range v {
				v[k] = strip(e)
			}
		case []any:
			for i, e := range v {
				v[i] = strip(e)
			}
		}
		return v
	}
	out := map[string]any{}
	for path, body := range testpack.Snapshot(t, dir) {
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out[path] = strip(v)
	}
	return out
}

func TestLibrivoxPersonDecisionsIgnoreRowOrder(t *testing.T) {
	projects := []LibrivoxProject{
		// An undated author and a reader of one name are one person.
		withReader(byAuthor(t, 99101, "Roe's Verses", "English", lvAuthor(5001, "Jane", "Roe")), 9001, "Volunteer One"),
		withReader(byAuthor(t, 99102, "Another Book", "English", lvAuthor(5002, "Pat", "Other")), 9002, "Jane Roe"),
		// An author who died before LibriVox and a reader of one name are two.
		withReader(byAuthor(t, 99103, "Old Sermons", "English", lvDated(5003, "John", "Doe", "1700", "1770")), 9001, "Volunteer One"),
		withReader(byAuthor(t, 99104, "Third Book", "English", lvAuthor(5004, "Kim", "Third")), 9004, "John Doe"),
		// Two new ids of one name with other years: the lower project's keeps
		// the plain name, the other is minted dated.
		withReader(byAuthor(t, 99105, "Grant's Tales", "English", lvDated(5005, "James", "Grant", "1822", "1887")), 9001, "Volunteer One"),
		withReader(byAuthor(t, 99106, "Grant's Letters", "English", lvDated(5006, "James", "Grant", "1802", "1879")), 9001, "Volunteer One"),
		// One id spelled two ways is one person, named as its lower project
		// spells it.
		withReader(byAuthor(t, 99107, "Mountain Notes", "English", lvAuthor(5007, "Ann", "Hill-Smith")), 9001, "Volunteer One"),
		withReader(byAuthor(t, 99108, "Valley Notes", "English", lvAuthor(5007, "Anne", "Hill-Smith")), 9001, "Volunteer One"),
	}
	// RunLibrivox reads a snapshot in project-id order, so the rows are
	// handed to the planner directly, in each order.
	run := func(order []LibrivoxProject) (string, Summary) {
		dataDir := t.TempDir()
		books := make([]sourceBook, 0, len(order))
		for _, pr := range order {
			books = append(books, librivoxBook(pr))
		}
		sum, err := runBooks(books, sourceLibrivox, Options{DataDir: dataDir, ImportDate: testImportDate}, nil,
			func(p *planner) { p.genres = librivoxGenreTable() })
		if err != nil {
			t.Fatalf("import: %v\nwarnings: %v", err, sum.Warnings)
		}
		return dataDir, sum
	}
	forward, fsum := run(projects)
	reversed := slices.Clone(projects)
	slices.Reverse(reversed)
	backward, bsum := run(reversed)
	if !reflect.DeepEqual(treeWithoutSources(t, forward), treeWithoutSources(t, backward)) {
		t.Error("the two row orders left different trees")
	}
	if fsum.NewPeople != bsum.NewPeople || fsum.LibrivoxReadersSplit != bsum.LibrivoxReadersSplit {
		t.Errorf("summaries differ: %+v / %+v", fsum, bsum)
	}
	for _, dir := range []string{forward, backward} {
		if p := personOf(t, dir, "jane-roe"); p.AuthorID != 5001 || p.ReaderID != 9002 {
			t.Errorf("jane-roe = %+v, want author 5001 and reader 9002", p)
		}
		if p := personOf(t, dir, "john-doe"); p.AuthorID != 5003 || p.ReaderID != 0 {
			t.Errorf("john-doe = %+v, want the author alone", p)
		}
		if p := personOf(t, dir, "john-doe-librivox-9004"); p.ReaderID != 9004 {
			t.Errorf("john-doe-librivox-9004 = %+v", p)
		}
		if got := workAuthorsOf(t, dir, "grants-letters"); !reflect.DeepEqual(got, []string{"james-grant-1802-1879"}) {
			t.Errorf("grants-letters authors = %v", got)
		}
		if got := workAuthorsOf(t, dir, "valley-notes"); !reflect.DeepEqual(got, []string{"ann-hill-smith"}) {
			t.Errorf("valley-notes authors = %v", got)
		}
		mustValidate(t, dir)
	}
}

func TestLibrivoxPersonRulesJudgeTheInitialsSurvivor(t *testing.T) {
	// "A.B. Kovacs" (1850-1900) resolves through the batch's initials step
	// onto the catalogued "AB Kovacs", a modern author: rule 2 must judge that
	// person - the one the import would credit - not the free a-b-kovacs the
	// bare slug names, which would have let the id be stamped on a stranger.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("ab-kovacs")] = testpack.PersonJSON(t, "ab-kovacs", "AB Kovacs")
	retailWork(t, files, "a-modern-thriller", "A Modern Thriller", "ab-kovacs", "2020")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthor(t, 99120, "Old Verses", "English", lvDated(5101, "A.B.", "Kovacs", "1850", "1900"))), dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "old-verses"); !reflect.DeepEqual(got, []string{"a-b-kovacs-1850-1900"}) {
		t.Fatalf("old-verses authors = %v\nnotes: %v", got, sum.Notes)
	}
	if p := personOf(t, dataDir, "ab-kovacs"); p.AuthorID != 0 {
		t.Errorf("ab-kovacs = %+v, want no LibriVox id", p)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, writeSnapshot(t, byAuthor(t, 99120, "Old Verses", "English", lvDated(5101, "A.B.", "Kovacs", "1850", "1900"))), dataDir)
}

func TestLibrivoxBirthYearAloneBoundsTheLife(t *testing.T) {
	// LibriVox states only a birth year (b. 1840): no 2020 first edition is
	// theirs, so the catalogued same-name author is someone else and the id is
	// minted dated; and no reader of 2015 is them either.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("anna-kerr")] = personFiles(t, "anna-kerr", "Anna Kerr", nil)
	retailWork(t, files, "a-new-thriller", "A New Thriller", "anna-kerr", "2020")
	seedTree(t, dataDir, files)
	snap := writeSnapshot(t,
		byAuthors(t, 99130, "Hymns of the Hills", lvDated(5201, "Anna", "Kerr", "1840", "")),
		withReader(byAuthors(t, 99131, "Field Notes", lvDated(5202, "Tom", "Birch", "1840", "")), 9201, "Tom Birch"))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "hymns-of-the-hills"); !reflect.DeepEqual(got, []string{"anna-kerr-b-1840"}) {
		t.Fatalf("hymns-of-the-hills authors = %v\nworklist: %v", got, sum.LibrivoxWorklist)
	}
	if p := personOf(t, dataDir, "tom-birch"); p.AuthorID != 5202 || p.ReaderID != 0 {
		t.Errorf("tom-birch = %+v, want the author alone", p)
	}
	if p := personOf(t, dataDir, "tom-birch-librivox-9201"); p.ReaderID != 9201 {
		t.Errorf("tom-birch-librivox-9201 = %+v", p)
	}
	mustValidate(t, dataDir)

	// A birth year that leaves the life open still attaches.
	open := t.TempDir()
	seedTree(t, open, files)
	runLibrivox(t, writeSnapshot(t, byAuthors(t, 99132, "Hymns Again", lvDated(5203, "Anna", "Kerr", "1960", ""))), open, allLibrivox)
	if got := workAuthorsOf(t, open, "hymns-again"); !reflect.DeepEqual(got, []string{"anna-kerr"}) {
		t.Errorf("hymns-again authors = %v", got)
	}
}

func TestLibrivoxTitleKeysOfAQualifiedGenericTitle(t *testing.T) {
	// "Poems (Ballads of a Cheechako)": the bare "Poems" names nothing, but
	// the qualifier names the book, so the full title still keys it.
	keys := librivoxTitleKeys("Poems (Ballads of a Cheechako)")
	if !keys[librivoxTitleKey("Poems (Ballads of a Cheechako)")] || keys[librivoxTitleKey("Poems")] {
		t.Errorf("keys = %v, want the full title's and not the bare base's", keys)
	}
	for _, generic := range []string{"Poems", "Poems (1915)", "Selected Poems"} {
		if keys := librivoxTitleKeys(generic); len(keys) != 0 {
			t.Errorf("%q keys = %v, want none", generic, keys)
		}
	}
	if keys := librivoxTitleKeys("Metamorphosis (1915)"); !keys[librivoxTitleKey("Metamorphosis")] || !keys[librivoxTitleKey("Metamorphosis (1915)")] {
		t.Errorf("Metamorphosis (1915) keys = %v", keys)
	}
}

func TestLibrivoxGenericTitleWithAQualifierIsATitleInCommon(t *testing.T) {
	// A catalogued author whose works are dated only past the id's death
	// shares "Poems (Ballads of a Cheechako)" with the project: the same poet.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("robert-w-service")] = personFiles(t, "robert-w-service", "Robert W. Service", nil)
	retailWork(t, files, "poems-ballads-of-a-cheechako", "Poems (Ballads of a Cheechako)", "robert-w-service", "1990")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99140, "Poems (Ballads of a Cheechako)", lvDated(5301, "Robert W.", "Service", "1874", "1958"))), dataDir, allLibrivox)
	if p := personOf(t, dataDir, "robert-w-service"); p.AuthorID != 5301 {
		t.Errorf("robert-w-service = %+v\nworklist: %v", p, sum.LibrivoxWorklist)
	}
}

func TestLibrivoxEditionsOfTheIDsOwnTextAreNoEvidence(t *testing.T) {
	// 5142 "Narrative of Sojourner Truth" (text 1850) by Olive Gilbert and
	// Sojourner Truth (1797-1883): the catalogue holds that book under the same
	// title, credited to Olive Gilbert (a co-author the project credits, by slug) and dated
	// only by a 2009 release. So its release dates date editions, and the
	// undated sojourner-truth, dated only by a 2018 release of "Ain't I a
	// Woman?", is the same person, not a worklist row.
	seed := func(t *testing.T) string {
		dataDir := t.TempDir()
		files := map[string]string{}
		files[personAddr("olive-gilbert")] = personFiles(t, "olive-gilbert", "Olive Gilbert", nil)
		files[personAddr("sojourner-truth")] = personFiles(t, "sojourner-truth", "Sojourner Truth", nil)
		retailWork(t, files, "the-narrative-of-sojourner-truth", "The Narrative of Sojourner Truth", "olive-gilbert", "")
		retailWork(t, files, "aint-i-a-woman", "Ain't I a Woman?", "sojourner-truth", "")
		// The translators' shapes, which an edition proves nothing for.
		files[personAddr("the-brothers-grimm")] = personFiles(t, "the-brothers-grimm", "The Brothers Grimm", nil)
		files[personAddr("margaret-hunt")] = personFiles(t, "margaret-hunt", "Margaret Hunt", nil)
		retailWork(t, files, "household-tales", "Household Tales", "the-brothers-grimm", "")
		retailWork(t, files, "grimms-fairy-stories", "Grimm's Fairy Stories", "margaret-hunt", "")
		files[personAddr("proclus")] = personFiles(t, "proclus", "Proclus", nil)
		files[personAddr("thomas-taylor")] = personFiles(t, "thomas-taylor", "Thomas Taylor", nil)
		retailWork(t, files, "the-elements-of-theology", "The Elements of Theology", "proclus", "")
		retailWork(t, files, "malamander", "Malamander", "thomas-taylor", "")
		files[personAddr("j-j-howard")] = personFiles(t, "j-j-howard", "J.J. Howard", nil)
		retailWork(t, files, "pugs-and-kisses", "Pugs and Kisses", "j-j-howard", "")
		files[personAddr("ovid")] = personFiles(t, "ovid", "Ovid", nil)
		retailWork(t, files, "metamorphoses", "Metamorphoses", "ovid", "")
		seedTree(t, dataDir, files)
		return dataDir
	}
	reasons := func(sum Summary) map[int]string {
		out := map[int]string{}
		for _, e := range sum.LibrivoxWorklist {
			out[e.Project] = e.Reason
		}
		return out
	}
	dated := func(pr LibrivoxProject, year string) LibrivoxProject {
		pr.CopyrightYear = lvText(year)
		return pr
	}
	truth := dated(byAuthors(t, 5142, "Narrative of Sojourner Truth",
		lvDated(2900, "Olive", "Gilbert", "1801", "1884"), lvDated(5706, "Sojourner", "Truth", "1797", "1883")), "1850")
	// A translator's project is another author's book: its editions say
	// nothing of the translator (Margaret Hunt 11939; Thomas Taylor 1758-1835,
	// against a modern novelist). And "Metamorphoses (Howard Version)" names one
	// translation, so 18450 (J. J. Howard, fl. 1807) holds as well.
	hunt := dated(byAuthor(t, 11939, "Household Tales", "English",
		lvDated(4098, "Jacob & Wilhelm", "Grimm", "1785", "1863"), lvDated(12708, "Margaret", "Hunt", "1831", "1912")), "1884")
	taylor := dated(byAuthor(t, 15500, "The Elements of Theology", "English",
		lvDated(6730, "", "Proclus", "412", "485"), lvDated(14134, "Thomas", "Taylor", "1758", "1835")), "1816")
	howard := dated(byAuthor(t, 18450, "Metamorphoses (Howard Version)", "English",
		lvDated(4959, "Publius (Ovid)", "Ovidius Naso", "c. 43 BC", "18 AD"), lvDated(17467, "J. J.", "Howard", "", "fl. 1807")), "1807")

	dataDir := seed(t)
	sum := runLibrivox(t, writeSnapshot(t, truth, hunt, taylor, howard), dataDir, allLibrivox)
	got := reasons(sum)
	if got[5142] == LibrivoxSameNameAuthor {
		t.Errorf("5142 held as same-name-author: %v", sum.LibrivoxWorklist)
	}
	for _, id := range []int{11939, 15500, 18450} {
		if got[id] != LibrivoxSameNameAuthor {
			t.Errorf("%d = %q, want held as same-name-author: %v", id, got[id], sum.LibrivoxWorklist)
		}
	}
	if p := personOf(t, dataDir, "thomas-taylor"); p.AuthorID != 0 {
		t.Errorf("thomas-taylor = %+v, want the novelist untouched", p)
	}

	// Without the edition, the release-dated person still holds the id.
	truth.Title = "The Life of Isabella Van Wagenen"
	dataDir = seed(t)
	if got := reasons(runLibrivox(t, writeSnapshot(t, truth), dataDir, allLibrivox)); got[5142] != LibrivoxSameNameAuthor {
		t.Errorf("without the edition 5142 = %q, want same-name-author", got[5142])
	}
}

func TestLibrivoxReaderFoldSplitOffIsNoMerge(t *testing.T) {
	// "Dr. Jane Roe" (9301) folds to "Jane Roe", a name reader 9300 holds, so
	// she is split onto a record of her own: no honorific merge happened, and
	// none is reported. A fold that does land is: "Dr. Kim Lee" (9302) claims
	// the catalogued narrator Kim Lee, who carries no reader id.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("kim-lee")] = personFiles(t, "kim-lee", "Kim Lee", nil)
	narratedWork(t, files, "a-retail-reading", "A Retail Reading", "kim-lee")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t,
		withReader(byAuthor(t, 99150, "First Readings", "English", lvAuthor(5401, "Ann", "One")), 9300, "Jane Roe"),
		withReader(byAuthor(t, 99151, "Second Readings", "English", lvAuthor(5402, "Bea", "Two")), 9301, "Dr. Jane Roe"),
		withReader(byAuthor(t, 99152, "Third Readings", "English", lvAuthor(5403, "Cy", "Three")), 9302, "Dr. Kim Lee"),
	), dataDir, allLibrivox)
	if p := personOf(t, dataDir, "jane-roe-librivox-9301"); p.ReaderID != 9301 {
		t.Fatalf("jane-roe-librivox-9301 = %+v", p)
	}
	if p := personOf(t, dataDir, "kim-lee"); p.ReaderID != 9302 {
		t.Errorf("kim-lee = %+v, want reader 9302", p)
	}
	if want := []string{"Dr. Kim Lee -> Kim Lee"}; !reflect.DeepEqual(sum.HonorificMerges, want) {
		t.Errorf("HonorificMerges = %v, want %v", sum.HonorificMerges, want)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxGenericTitleShowsNoEditions(t *testing.T) {
	// "Poems" (text 1850) by John Smith (1800-1870) beside a catalogued,
	// undated John Smith whose own "Poems" and thriller are dated only by
	// modern releases: a generic title is no title in common, so it shows no
	// edition either, and the id is held rather than stamped on the modern
	// poet.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("john-smith")] = personFiles(t, "john-smith", "John Smith", nil)
	retailWork(t, files, "poems", "Poems", "john-smith", "")
	retailWork(t, files, "a-modern-thriller", "A Modern Thriller", "john-smith", "")
	seedTree(t, dataDir, files)
	pr := byAuthors(t, 99200, "Poems", lvDated(5501, "John", "Smith", "1800", "1870"))
	pr.CopyrightYear = lvText("1850")
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxWorklist[0].Reason != LibrivoxSameNameAuthor {
		t.Errorf("worklist %v, want 99200 held as same-name-author", sum.LibrivoxWorklist)
	}
	if p := personOf(t, dataDir, "john-smith"); p.AuthorID != 0 || p.BirthYear != 0 {
		t.Errorf("john-smith = %+v, want the modern poet untouched", p)
	}
}

func TestLibrivoxVariantMovedDeadAuthorKeepsReadersOff(t *testing.T) {
	// 5601 "Henrietta Elizabeth Marshall" (1867-1941) is moved onto the
	// catalogued "Henrietta Marshall" by rule 1 of librivoxauthors.go, after
	// the person rules first pinned it to a new record: the person it finally
	// lands on is the author record rule 3 keeps a same-name reader off.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("henrietta-marshall")] = personFiles(t, "henrietta-marshall", "Henrietta Marshall", nil)
	retailWork(t, files, "our-island-story", "Our Island Story", "henrietta-marshall", "")
	seedTree(t, dataDir, files)
	runLibrivox(t, writeSnapshot(t,
		byAuthors(t, 99300, "Our Island Story", lvDated(5601, "Henrietta Elizabeth", "Marshall", "1867", "1941")),
		withReader(byAuthors(t, 99301, "Something Else", lvAuthor(5602, "Pat", "Other")), 9500, "Henrietta Marshall"),
	), dataDir, allLibrivox)
	if p := personOf(t, dataDir, "henrietta-marshall"); p.AuthorID != 5601 || p.ReaderID != 0 {
		t.Errorf("henrietta-marshall = %+v, want the author alone", p)
	}
	if p := personOf(t, dataDir, "henrietta-marshall-librivox-9500"); p.ReaderID != 9500 {
		t.Errorf("henrietta-marshall-librivox-9500 = %+v", p)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxFlourishedAuthorKeepsReadersOff(t *testing.T) {
	// An author LibriVox dates by a flourished year alone (fl. 1807) read
	// nothing for LibriVox either: a same-name reader is split off them.
	dataDir := t.TempDir()
	runLibrivox(t, writeSnapshot(t,
		byAuthors(t, 99500, "Old Tracts", lvDated(5701, "Richard", "Wilsonne", "", "fl. 1807")),
		withReader(byAuthors(t, 99501, "Another", lvAuthor(5702, "Pat", "Other")), 9700, "Richard Wilsonne"),
	), dataDir, allLibrivox)
	if p := personOf(t, dataDir, "richard-wilsonne"); p.AuthorID != 5701 || p.ReaderID != 0 {
		t.Errorf("richard-wilsonne = %+v, want the author alone", p)
	}
	if p := personOf(t, dataDir, "richard-wilsonne-librivox-9700"); p.ReaderID != 9700 {
		t.Errorf("richard-wilsonne-librivox-9700 = %+v", p)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxReaderSpelledTwoWaysIsOnePerson(t *testing.T) {
	// A reader id is decided by its lowest project's spelling. When that
	// project never lands (here an unsluggable author with no LibriVox id, a worklist row), the
	// reader's other project creates them under that spelling - their own
	// name's record (9600), or their split one (9601, whose plain name 9599
	// holds) - never a narrator no record backs.
	cyrillic := func(id int) LibrivoxPerson {
		return LibrivoxPerson{ID: lvInt(id), FirstName: "Йордан", LastName: "Йовков"}
	}
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t,
		withReader(byAuthors(t, 99398, "First Words", lvAuthor(5800, "Ann", "One")), 9599, "Petra"),
		withReader(byAuthors(t, 99399, "Legends", cyrillic(0)), 9601, "Petra"),
		withReader(byAuthors(t, 99400, "More Legends", cyrillic(0)), 9600, "Martina"),
		withReader(byAuthors(t, 99401, "Other Book", lvAuthor(5801, "Pat", "Other")), 9600, "Martina S."),
		withReader(byAuthors(t, 99402, "Third Book", lvAuthor(5802, "Kim", "Third")), 9601, "Petra K."),
	), dataDir, allLibrivox)
	if len(sum.LibrivoxWorklist) != 2 {
		t.Fatalf("worklist %v, want the two unsluggable rows", sum.LibrivoxWorklist)
	}
	if p := personOf(t, dataDir, "martina"); p.Name != "Martina" || p.ReaderID != 9600 {
		t.Errorf("martina = %+v, want reader 9600", p)
	}
	if p := personOf(t, dataDir, "petra-librivox-9601"); p.Name != "Petra" || p.ReaderID != 9601 {
		t.Errorf("petra-librivox-9601 = %+v, want reader 9601 under their first spelling", p)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxEditionByAnotherWriterOfTheSurnameIsNoEvidence(t *testing.T) {
	// Mary Webb (1881-1927) wrote "Garden Verses" alone. The catalogue holds a
	// release-only "Garden Verses" by Precious Webb, a modern writer the project
	// does not credit, and an undated modern mary-webb with a 2020 release: a
	// shared surname is no edition of Mary Webb's text, so the id is held, never
	// stamped on the modern writer.
	dataDir := t.TempDir()
	files := map[string]string{}
	files[personAddr("precious-webb")] = personFiles(t, "precious-webb", "Precious Webb", nil)
	files[personAddr("mary-webb")] = personFiles(t, "mary-webb", "Mary Webb", nil)
	retailWork(t, files, "garden-verses", "Garden Verses", "precious-webb", "")
	retailWork(t, files, "a-modern-romance", "A Modern Romance", "mary-webb", "")
	seedTree(t, dataDir, files)
	pr := byAuthors(t, 99160, "Garden Verses", lvDated(5501, "Mary", "Webb", "1881", "1927"))
	pr.CopyrightYear = "1910"
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxWorklist[0].Reason != LibrivoxSameNameAuthor {
		t.Fatalf("worklist = %v, want 99160 held as same-name-author", sum.LibrivoxWorklist)
	}
	if p := personOf(t, dataDir, "mary-webb"); p.AuthorID != 0 {
		t.Errorf("mary-webb = %+v, want the modern writer untouched", p)
	}
}

func TestLibrivoxEditionCoAuthorIsThePersonNotANamesake(t *testing.T) {
	// The edition of 5142's text counts only when it is credited to the very
	// person the project credits beside Sojourner Truth: Olive Gilbert's own
	// catalogued id (2900) counts; a namesake carrying another author id, or
	// stating other years, is someone else's "Narrative", so Truth is held.
	cases := []struct {
		name    string
		gilbert map[string]any
		held    bool
	}{
		{"catalogued id", map[string]any{"librivox_author_id": 2900}, false},
		{"another author id", map[string]any{"librivox_author_id": 7777}, true},
		{"other years", map[string]any{"birth_year": 1950, "death_year": 2020}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{}
			files[personAddr("olive-gilbert")] = personFiles(t, "olive-gilbert", "Olive Gilbert", tc.gilbert)
			files[personAddr("sojourner-truth")] = personFiles(t, "sojourner-truth", "Sojourner Truth", nil)
			retailWork(t, files, "the-narrative-of-sojourner-truth", "The Narrative of Sojourner Truth", "olive-gilbert", "")
			retailWork(t, files, "aint-i-a-woman", "Ain't I a Woman?", "sojourner-truth", "")
			seedTree(t, dataDir, files)
			truth := byAuthors(t, 5142, "Narrative of Sojourner Truth",
				lvDated(2900, "Olive", "Gilbert", "1801", "1884"), lvDated(5706, "Sojourner", "Truth", "1797", "1883"))
			truth.CopyrightYear = lvText("1850")
			sum := runLibrivox(t, writeSnapshot(t, truth), dataDir, allLibrivox)
			held := false
			for _, e := range sum.LibrivoxWorklist {
				if e.Project == 5142 && e.Reason == LibrivoxSameNameAuthor {
					held = true
				}
			}
			if held != tc.held {
				t.Errorf("5142 held = %v, want %v: %v", held, tc.held, sum.LibrivoxWorklist)
			}
		})
	}
}

func TestLibrivoxAnthologyEditionReleasesNoOtherContributor(t *testing.T) {
	// "The Book of Humorous Verse" (text 1920) credits Carolyn Wells and two
	// poets. Its catalogued release-only edition credits Carolyn Wells, but an
	// anthology's edition speaks for no other contributor: the undated
	// catalogued oliver-herford, dated only by a 2020 release, still holds
	// Oliver Herford (1860-1935). The same edition of a two-author book
	// releases the hold (Sojourner Truth's shape).
	seed := func(t *testing.T) string {
		dataDir := t.TempDir()
		files := map[string]string{}
		files[personAddr("carolyn-wells")] = personFiles(t, "carolyn-wells", "Carolyn Wells", nil)
		files[personAddr("oliver-herford")] = personFiles(t, "oliver-herford", "Oliver Herford", nil)
		retailWork(t, files, "the-book-of-humorous-verse", "The Book of Humorous Verse", "carolyn-wells", "")
		retailWork(t, files, "a-modern-cat-book", "A Modern Cat Book", "oliver-herford", "")
		seedTree(t, dataDir, files)
		return dataDir
	}
	held := func(sum Summary, id int) bool {
		for _, e := range sum.LibrivoxWorklist {
			if e.Project == id && e.Reason == LibrivoxSameNameAuthor {
				return true
			}
		}
		return false
	}
	anthology := byAuthors(t, 99170, "The Book of Humorous Verse",
		lvDated(5601, "Carolyn", "Wells", "1862", "1942"), lvDated(5602, "Oliver", "Herford", "1860", "1935"),
		lvDated(5603, "Edward", "Lear", "1812", "1888"))
	anthology.CopyrightYear = "1920"
	sum := runLibrivox(t, writeSnapshot(t, anthology), seed(t), allLibrivox)
	if !held(sum, 99170) {
		t.Errorf("worklist = %v, want 99170 held as same-name-author", sum.LibrivoxWorklist)
	}

	pair := byAuthors(t, 99171, "The Book of Humorous Verse",
		lvDated(5601, "Carolyn", "Wells", "1862", "1942"), lvDated(5602, "Oliver", "Herford", "1860", "1935"))
	pair.CopyrightYear = "1920"
	if sum := runLibrivox(t, writeSnapshot(t, pair), seed(t), allLibrivox); held(sum, 99171) {
		t.Errorf("worklist = %v, want the two-author book released", sum.LibrivoxWorklist)
	}
}
