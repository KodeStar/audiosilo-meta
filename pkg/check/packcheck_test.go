package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Every record a pack test uses is built by one of the functions below, so each
// record SHAPE is written once in the package: a field the schema gains is added
// in one place, and no fixture can quietly drift into a different spelling of
// "a valid person".

// pkPerson returns a valid person record. The id has to BE the slug of the name
// (checkPersonSlug), so both are given rather than derived.
func pkPerson(id, name string) string {
	return `{"id":` + strconv.Quote(id) + `,"license":"CC0-1.0","name":` + strconv.Quote(name) +
		`,"sources":[{"type":"user"}]}`
}

// pkRec returns a valid recording record under work, narrated by narrator-one.
func pkRec(id, work, lang string) string {
	return `{"abridged":false,"id":` + strconv.Quote(id) + `,"language":` + strconv.Quote(lang) +
		`,"license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":` +
		strconv.Quote(work) + `}`
}

// pkWorkTitled returns a valid work record by author-one. The title is a
// parameter because it is not inert: two works with one title and one author
// are what checkIdentityEqualWorks reports, so a fixture that wants many works
// and no advisories has to give them distinct titles (see pkTitled).
func pkWorkTitled(slug, title string) string {
	return `{"authors":["author-one"],"id":` + strconv.Quote(slug) +
		`,"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":` +
		strconv.Quote(title) + `}`
}

// pkWork returns a minimal valid work record for slug. They are all titled the
// same, so any two of them in one tree are one book under two ids as far as
// checkIdentityEqualWorks is concerned - which costs the cap and bound tests
// nothing (they assert on problems and ignore advisories) and would drown a
// fixture that is ABOUT advisories, so that one uses pkTitled.
func pkWork(slug string) string { return pkWorkTitled(slug, "T") }

// pkTitled returns a valid work whose title is its own slug, so a fixture can
// hold many works without tripping the identity-equal-works advisory on every
// pair of them - which would bury the advisories it is actually about.
func pkTitled(slug string) string { return pkWorkTitled(slug, slug) }

// The records below are the fixture catalogue every pack test builds on: one
// work with one recording, its two credits, one series, and the work's two
// community sidecars. They are the named instances of the builders above; the
// tests that mutate them do it with strings.Replace, so their exact bytes are
// part of the fixture.
var (
	pkAuthorOne   = pkPerson("author-one", "Author One")
	pkNarratorOne = pkPerson("narrator-one", "Narrator One")
	pkWorkOne     = pkWorkTitled("book-one", "Book One")
	pkRecOne      = pkRec("rec-one", "book-one", "en")
	pkSeriesOne   = `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
)

// jsonObject renders a key -> raw-JSON map as an object with sorted keys.
func jsonObject(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(strconv.Quote(k) + ":" + m[k])
	}
	b.WriteString("}")
	return b.String()
}

// packOf renders a pack file around the given entries.
func packOf(entries map[string]string) string { return `{"entries":` + jsonObject(entries) + `}` }

// composite splices a recordings map into a work record, producing a
// works-family pack entry.
func composite(work string, recs map[string]string) string {
	if len(recs) == 0 {
		return work
	}
	return strings.TrimSuffix(work, "}") + `,"recordings":` + jsonObject(recs) + `}`
}

// packValid returns a minimal, fully valid PACK-layout data tree
// (relpath -> content): the pack twin of baseValid(), plus the works-community
// sidecars that legacy keeps inside the work's directory. works and
// works-community carry a directory level; people and series are flat.
func packValid() map[string]string {
	return map[string]string{
		"people/0.json": packOf(map[string]string{
			"author-one":   pkAuthorOne,
			"narrator-one": pkNarratorOne,
		}),
		"works/0/0.json": packOf(map[string]string{
			"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
		}),
		"works-community/0/0.json": packOf(map[string]string{
			"book-one": `{"characters":` + validCharacters("book-one") + `,"recaps":` + validRecaps("book-one") + `}`,
		}),
		"series/0.json": packOf(map[string]string{"series-one": pkSeriesOne}),
	}
}

func hasProblem(ps []Problem, want string) bool {
	for _, p := range ps {
		if strings.Contains(p.String(), want) {
			return true
		}
	}
	return false
}

// TestPackLoadValid is the passing fixture for the whole pack walker: a
// well-formed tree of all four families loads clean and lands in the Catalog.
func TestPackLoadValid(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("valid pack tree reported problems:\n%s", joinProblems(res.Problems))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("valid pack tree reported advisories:\n%s", joinProblems(res.Warnings))
	}
	cat := res.Catalog
	if len(cat.Works) != 1 || len(cat.People) != 2 || len(cat.Series) != 1 ||
		len(cat.Characters) != 1 || len(cat.Recaps) != 1 {
		t.Fatalf("unexpected catalog counts: %d works, %d people, %d series, %d characters, %d recaps",
			len(cat.Works), len(cat.People), len(cat.Series), len(cat.Characters), len(cat.Recaps))
	}
	if len(cat.Works[0].Recordings) != 1 || cat.Works[0].Recordings[0].ID != "rec-one" {
		t.Errorf("composite recordings not attached: %+v", cat.Works[0].Recordings)
	}
}

// TestPackRepackingIsCatalogPreserving pins that the SHAPE of the tree is not
// something the catalog can see: the same records split across a different set
// of packs, in a different number of directories, load to the same Catalog. It
// is what lets metafmt split and relocate freely, and what the migration relied
// on when it repacked every record in the repository.
func TestPackRepackingIsCatalogPreserving(t *testing.T) {
	oneDir, splitDir := t.TempDir(), t.TempDir()
	writeTree(t, oneDir, packValid())

	// The same entries, spread over two works packs and two people packs.
	split := packValid()
	split["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
	})
	split["people/0.json"] = packOf(map[string]string{"author-one": pkAuthorOne})
	split["people/narrator-one.json"] = packOf(map[string]string{"narrator-one": pkNarratorOne})
	writeTree(t, splitDir, split)

	oneRes, splitRes := Load(oneDir), Load(splitDir)
	if !oneRes.OK() {
		t.Fatalf("single-pack fixture reported problems:\n%s", joinProblems(oneRes.Problems))
	}
	if !splitRes.OK() {
		t.Fatalf("split fixture reported problems:\n%s", joinProblems(splitRes.Problems))
	}
	if got, want := catalogDigest(t, splitRes.Catalog), catalogDigest(t, oneRes.Catalog); got != want {
		t.Errorf("repacking changed the catalog\nsplit:\n%s\nsingle:\n%s", got, want)
	}
}

// catalogDigest renders a catalog in a layout-independent, order-independent
// form so two loads can be compared byte for byte.
func catalogDigest(t *testing.T, cat *model.Catalog) string {
	t.Helper()
	type workDigest struct {
		Work       *model.Work
		Recordings []*model.Recording
	}
	works := make([]workDigest, 0, len(cat.Works))
	for _, w := range cat.Works {
		works = append(works, workDigest{Work: w, Recordings: w.Recordings})
	}
	sort.Slice(works, func(i, j int) bool { return works[i].Work.ID < works[j].Work.ID })

	people := append([]*model.Person(nil), cat.People...)
	sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
	series := append([]*model.Series(nil), cat.Series...)
	sort.Slice(series, func(i, j int) bool { return series[i].ID < series[j].ID })
	chars := append([]*model.Characters(nil), cat.Characters...)
	sort.Slice(chars, func(i, j int) bool { return chars[i].Work < chars[j].Work })
	recaps := append([]*model.Recaps(nil), cat.Recaps...)
	sort.Slice(recaps, func(i, j int) bool { return recaps[i].Work < recaps[j].Work })

	out, err := json.MarshalIndent(struct {
		Works      []workDigest
		People     []*model.Person
		Series     []*model.Series
		Characters []*model.Characters
		Recaps     []*model.Recaps
	}{works, people, series, chars, recaps}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A family with no data at all is absent, not empty: no root directory, no
// packs, nothing to report. works-community is exactly that for most of the
// tree's history, so the load has to treat it as "no sidecars" rather than as a
// missing family.
func TestPackAbsentFamilyLoads(t *testing.T) {
	files := packValid()
	delete(files, "works-community/0/0.json")

	res := Load(mustWrite(t, files))
	if !res.OK() {
		t.Fatalf("a tree without works-community reported problems:\n%s", joinProblems(res.Problems))
	}
	if len(res.Catalog.Characters) != 0 || len(res.Catalog.Recaps) != 0 {
		t.Errorf("sidecars appeared from nowhere: %d characters, %d recaps",
			len(res.Catalog.Characters), len(res.Catalog.Recaps))
	}
	if len(res.Catalog.Works) != 1 {
		t.Errorf("the rest of the tree did not load: %d works", len(res.Catalog.Works))
	}
}

// TestLegacyTreeIsRejected is what is left of the file-per-entity layout: the
// reader is gone, so a tree still in that shape must fail LOUDLY at both ends -
// metacheck reports it and names the fix, and every writer refuses to open it.
// Silence would mean a converted repository quietly ignoring records that a
// stale branch or an old backup put back.
func TestLegacyTreeIsRejected(t *testing.T) {
	dir := mustWrite(t, map[string]string{
		"works/bo/book-one/work.json":               pkWorkOne,
		"works/bo/book-one/recordings/rec-one.json": pkRecOne,
		"people/au/author-one.json":                 pkAuthorOne,
	})

	res := Load(dir)
	if res.OK() {
		t.Fatalf("a legacy tree loaded clean; catalog: %d works", len(res.Catalog.Works))
	}
	if len(res.Catalog.Works) != 0 || len(res.Catalog.People) != 0 {
		t.Errorf("legacy records reached the catalog: %d works, %d people",
			len(res.Catalog.Works), len(res.Catalog.People))
	}
	for _, want := range []string{"works", "people"} {
		if !hasProblem(res.Problems, want+": family is not in the pack layout") {
			t.Errorf("no legacy-layout problem for %s; problems:\n%s", want, joinProblems(res.Problems))
		}
	}
	if !hasProblem(res.Problems, "cmd/metamigrate") {
		t.Errorf("the problems do not name the fix; problems:\n%s", joinProblems(res.Problems))
	}
	if _, err := pack.OpenFor(dir, pack.FamilyWorks); !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("a writer opened a legacy tree: err = %v, want pack.ErrLegacyLayout", err)
	}
}

// TestPackProblemPaths pins the reporting format: a pack problem locates itself
// from the data root down to the entry, the nested recording, or the sidecar
// member it came from.
func TestPackProblemPaths(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string // the exact Problem.Path expected
	}{
		{
			name: "entry",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(
						strings.Replace(pkWorkOne, `,"title":"Book One"`, "", 1),
						map[string]string{"rec-one": pkRecOne}),
				})
			},
			want: "works/0/0.json: entry book-one",
		},
		{
			name: "nested recording",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{
						"rec-one": strings.Replace(pkRecOne, `"narrators":["narrator-one"],`, "", 1),
					}),
				})
			},
			want: "works/0/0.json: entry book-one: recording rec-one",
		},
		{
			name: "community member",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"characters":` +
						strings.Replace(validCharacters("book-one"), `"CC-BY-SA-3.0"`, `"CC0-1.0"`, 1) + `}`,
				})
			},
			want: "works-community/0/0.json: entry book-one: characters",
		},
		{
			name: "whole pack",
			mutate: func(f map[string]string) {
				f["works/0/m.json"] = packOf(nil)
			},
			want: "works/0/m.json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := packValid()
			c.mutate(files)
			writeTree(t, dir, files)
			res := Load(dir)
			if res.OK() {
				t.Fatalf("expected a problem at %q, got none", c.want)
			}
			for _, p := range res.Problems {
				if p.Path == c.want {
					return
				}
			}
			t.Errorf("no problem reported at path %q; problems:\n%s", c.want, joinProblems(res.Problems))
		})
	}
}

func TestPackRuleViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		// --- placement ---------------------------------------------------
		{
			name: "entry outside its pack's range",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one":   composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
					"zebra-book": pkWork("zebra-book"),
				})
				f["works/0/m.json"] = packOf(map[string]string{"m-book": pkWork("m-book")})
			},
			want: "entry is outside the pack's range [0, m)",
		},
		{
			name: "pack outside its directory's range",
			mutate: func(f map[string]string) {
				f["works/0/z.json"] = packOf(map[string]string{"z-book": pkWork("z-book")})
				f["works/m/m.json"] = packOf(map[string]string{"m-book": pkWork("m-book")})
			},
			want: "pack is outside its directory's range [0, m)",
		},
		// --- caps ----------------------------------------------------------
		{
			name: "pack over the hard size cap",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(bigWork("book-one", 300_000), map[string]string{"rec-one": pkRecOne}),
					"book-two": bigWork("book-two", 300_000),
				})
			},
			want: "over the hard cap of 524288",
		},
		{
			name: "pack over the entry cap",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(manyWorks(1001))
			},
			want: "pack holds 1001 entries, over the works entry cap of 1000",
		},
		{
			name: "directory over the pack cap",
			mutate: func(f map[string]string) {
				delete(f, "works/0/0.json")
				for rel, body := range manyPacks("works/0", 513) {
					f[rel] = body
				}
			},
			want: "directory holds 513 packs, over the cap of 512",
		},
		{
			name: "flat family over the pack cap",
			mutate: func(f map[string]string) {
				delete(f, "series/0.json")
				for rel, body := range manySeriesPacks(513) {
					f[rel] = body
				}
			},
			want: "over the per-directory cap of 512: it has to gain a directory level",
		},
		// --- bound validity ------------------------------------------------
		{
			name: "pack name is not a slug",
			mutate: func(f map[string]string) {
				f["works/0/Book.json"] = packOf(map[string]string{"book-two": pkWork("book-two")})
			},
			want: `pack name "Book" is not a valid slug bound`,
		},
		{
			name: "directory name is not a slug",
			mutate: func(f map[string]string) {
				f["works/Mm/n.json"] = packOf(map[string]string{"n-book": pkWork("n-book")})
			},
			want: `directory name "Mm" is not a valid slug bound`,
		},
		{
			name: "family's first pack is not the reserved minimum",
			mutate: func(f map[string]string) {
				f["people/a.json"] = f["people/0.json"]
				delete(f, "people/0.json")
			},
			want: `the family's first pack must be named "0"`,
		},
		{
			name: "family's first directory is not the reserved minimum",
			mutate: func(f map[string]string) {
				f["works/a/a.json"] = f["works/0/0.json"]
				delete(f, "works/0/0.json")
			},
			want: `the family's first directory must be named "0"`,
		},
		{
			name: "sibling bounds do not increase",
			mutate: func(f map[string]string) {
				f["works/m/0.json"] = packOf(map[string]string{"book-two": pkWork("book-two")})
			},
			want: `bound "0" does not increase on works/0/0.json`,
		},
		{
			name: "directory bound is not its first pack's bound",
			mutate: func(f map[string]string) {
				f["works/m/n.json"] = packOf(map[string]string{"n-book": pkWork("n-book")})
			},
			want: `directory bound "m" must equal its first pack's bound "n"`,
		},
		{
			name:   "empty pack",
			mutate: func(f map[string]string) { f["works/0/m.json"] = packOf(nil) },
			want:   "pack holds no entries",
		},
		{
			name: "works pack without a directory level",
			mutate: func(f map[string]string) {
				f["works/m.json"] = packOf(map[string]string{"m-book": pkWork("m-book")})
			},
			want: "but this family carries a directory level",
		},
		{
			name: "flat family mixes root packs and directory packs",
			mutate: func(f map[string]string) {
				f["series/m/m.json"] = packOf(map[string]string{
					"m-series": `{"id":"m-series","license":"CC0-1.0","name":"M","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`,
				})
			},
			want: "family mixes packs directly under its root (1) with packs in directories (1)",
		},
		{
			name: "json file that is not a pack",
			mutate: func(f map[string]string) {
				f["works/0/0/stray.json"] = `{}`
			},
			want: "unrecognized location",
		},
		{
			name: "pack wrapper carries more than entries",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = `{"entries":{},"version":1}`
			},
			want: "invalid pack",
		},
		// --- key/id agreement ----------------------------------------------
		{
			name: "entry key does not match the work id",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(strings.Replace(pkWorkOne, `"id":"book-one"`, `"id":"book-other"`, 1),
						map[string]string{"rec-one": pkRecOne}),
				})
			},
			want: `id "book-other" does not match its entry key "book-one"`,
		},
		{
			name: "entry key does not match the person id",
			mutate: func(f map[string]string) {
				f["people/0.json"] = packOf(map[string]string{
					"author-one":   strings.Replace(pkAuthorOne, `"id":"author-one"`, `"id":"someone-else"`, 1),
					"narrator-one": pkNarratorOne,
				})
			},
			want: `id "someone-else" does not match its entry key "author-one"`,
		},
		{
			name: "entry key does not match the series id",
			mutate: func(f map[string]string) {
				f["series/0.json"] = packOf(map[string]string{
					"series-one": strings.Replace(pkSeriesOne, `"id":"series-one"`, `"id":"series-two"`, 1),
				})
			},
			want: `id "series-two" does not match its entry key "series-one"`,
		},
		{
			name: "recordings map key does not match the recording id",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{"rec-two": pkRecOne}),
				})
			},
			want: `id "rec-one" does not match its recordings map key "rec-two"`,
		},
		{
			name: "recording work backref does not match the entry key",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{
						"rec-one": strings.Replace(pkRecOne, `"work":"book-one"`, `"work":"book-other"`, 1),
					}),
				})
			},
			want: `work "book-other" must equal the parent entry key "book-one"`,
		},
		{
			name: "characters work backref does not match the entry key",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"characters":` + validCharacters("book-other") + `}`,
				})
			},
			want: `work "book-other" must equal the entry key "book-one"`,
		},
		{
			name: "recaps work backref does not match the entry key",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"recaps":` + validRecaps("book-other") + `}`,
				})
			},
			want: `work "book-other" must equal the entry key "book-one"`,
		},
		// The next two are the works-community wrapper schema's own rules
		// (minProperties, additionalProperties), reached because the walker
		// validates an entry THROUGH its family wrapper rather than through a
		// hand-written Go equivalent.
		{
			name: "community entry holds neither sidecar",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{"book-one": `{}`})
			},
			want: "works-community/0/0.json: entry book-one: (root): minProperties: got 0, want 1",
		},
		{
			name: "community entry holds an unknown member",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"notes":{},"recaps":` + validRecaps("book-one") + `}`,
				})
			},
			want: "additional properties 'notes' not allowed",
		},
		// --- license lock (schema-enforced, reported on the pack path) ------
		{
			name: "share-alike license on a works entry",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(strings.Replace(pkWorkOne, `"CC0-1.0"`, `"CC-BY-SA-3.0"`, 1),
						map[string]string{"rec-one": pkRecOne}),
				})
			},
			want: "works/0/0.json: entry book-one: /license",
		},
		{
			name: "share-alike license on a nested recording",
			mutate: func(f map[string]string) {
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{
						"rec-one": strings.Replace(pkRecOne, `"license":"CC0-1.0"`, `"license":"CC-BY-SA-3.0"`, 1),
					}),
				})
			},
			want: "works/0/0.json: entry book-one: recording rec-one: /license",
		},
		{
			name: "CC0 license on a community sidecar",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"recaps":` + strings.Replace(validRecaps("book-one"), `"CC-BY-SA-3.0"`, `"CC0-1.0"`, 1) + `}`,
				})
			},
			want: "works-community/0/0.json: entry book-one: recaps: /license",
		},
		// --- the cross-record rules, unchanged, over a pack-loaded Catalog --
		{
			name: "missing narrator",
			mutate: func(f map[string]string) {
				f["people/0.json"] = packOf(map[string]string{"author-one": pkAuthorOne})
			},
			want: `narrator "narrator-one" does not exist`,
		},
		{
			name: "duplicate ASIN across sibling recordings",
			mutate: func(f map[string]string) {
				withASIN := func(id string) string {
					return strings.Replace(
						strings.Replace(pkRecOne, `"id":"rec-one"`, `"id":"`+id+`"`, 1),
						`"abridged":false,`, `"abridged":false,"asin":[{"asin":"B000000001","region":"us"}],`, 1)
				}
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{
						"rec-one": withASIN("rec-one"),
						"rec-two": withASIN("rec-two"),
					}),
				})
			},
			want: "duplicate ASIN B000000001",
		},
		{
			name: "chapters not strictly increasing",
			mutate: func(f map[string]string) {
				rec := strings.Replace(pkRecOne, `"abridged":false,`,
					`"abridged":false,"chapters":[{"title":"One","start_ms":0,"length_ms":10},{"title":"Two","start_ms":0,"length_ms":10}],`, 1)
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{"rec-one": rec}),
				})
			},
			want: "is not greater than previous",
		},
		{
			name: "genres not sorted",
			mutate: func(f map[string]string) {
				work := strings.Replace(pkWorkOne, `"id":"book-one"`, `"genres":["science-fiction","fantasy"],"id":"book-one"`, 1)
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(work, map[string]string{"rec-one": pkRecOne}),
				})
			},
			want: "genres must be sorted",
		},
		{
			name: "duplicate series position",
			mutate: func(f map[string]string) {
				f["series/0.json"] = packOf(map[string]string{
					"series-one": `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"},{"position":"1","work":"book-two"}]}`,
				})
			},
			want: `duplicate series position "1"`,
		},
		{
			name: "duplicate character id within an entry",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"characters":{"characters":[{"id":"hero","name":"Hero","reveal":{"chapter":1}},{"id":"hero","name":"Twin","reveal":{"chapter":2}}],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"book-one"}}`,
				})
			},
			want: `duplicate character id "hero"`,
		},
		{
			name: "duplicate recap through-position within an entry",
			mutate: func(f map[string]string) {
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"recaps":{"license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}},{"text":"B.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}}`,
				})
			},
			want: "duplicate recap through chapter 3",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := packValid()
			c.mutate(files)
			writeTree(t, dir, files)
			res := Load(dir)
			if res.OK() {
				t.Fatalf("expected a problem containing %q, got none", c.want)
			}
			if !hasProblem(res.Problems, c.want) {
				t.Errorf("no problem contained %q; problems:\n%s", c.want, joinProblems(res.Problems))
			}
		})
	}
}

// TestPackCapsAtTheLimit is the passing half of the cap rules: a pack, a
// directory and a flat family exactly AT their caps are legal, so the rules
// only ever fire on the far side of the boundary.
func TestPackCapsAtTheLimit(t *testing.T) {
	works, _ := pack.Def(pack.FamilyWorks)
	cases := map[string]func(map[string]string){
		"pack at the entry cap": func(f map[string]string) {
			f["works/0/0.json"] = packOf(manyWorks(works.Caps.Entries))
		},
		"directory at the pack cap": func(f map[string]string) {
			delete(f, "works/0/0.json")
			for rel, body := range manyPacks("works/0", pack.DirPackCap) {
				f[rel] = body
			}
		},
		"flat family at the pack cap": func(f map[string]string) {
			delete(f, "series/0.json")
			for rel, body := range manySeriesPacks(pack.DirPackCap) {
				f[rel] = body
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			files := packValid()
			mutate(files)
			writeTree(t, dir, files)
			res := Load(dir)
			if !res.OK() {
				t.Errorf("a tree at its caps reported problems:\n%s", joinProblems(res.Problems))
			}
		})
	}
}

// TestPackEntryFailureIsLocal guards against a composite cascading: a recording
// the schema rejected must not take its work - and everything referencing that
// work - out of the catalog with it, the way one bad file never affected its
// siblings in the legacy layout.
func TestPackEntryFailureIsLocal(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{
			"rec-one": strings.Replace(pkRecOne, `"language":"en"`, `"language":"en","runtime_min":"nope"`, 1),
		}),
	})
	writeTree(t, dir, files)

	res := Load(dir)
	if !hasProblem(res.Problems, "works/0/0.json: entry book-one: recording rec-one: /runtime_min") {
		t.Errorf("expected the recording's own type problem; problems:\n%s", joinProblems(res.Problems))
	}
	for _, p := range res.Problems {
		if strings.Contains(p.Msg, `"book-one" does not exist`) {
			t.Errorf("a bad recording dropped its work from the catalog: %s", p)
		}
	}
	if len(res.Catalog.Works) != 1 {
		t.Errorf("expected the work to survive its bad recording, got %d works", len(res.Catalog.Works))
	}
}

// TestPackSingleEntryExemption covers the sign-off addition: an entry bigger
// than the hard size cap cannot be split out of its pack, so a single-entry
// pack is exempt from the SIZE cap and gets an advisory instead of a failure.
func TestPackSingleEntryExemption(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(bigWork("book-one", 600_000), map[string]string{"rec-one": pkRecOne}),
	})
	writeTree(t, dir, files)

	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a single oversized entry must not fail the check:\n%s", joinProblems(res.Problems))
	}
	if !hasProblem(res.Warnings, "works/0/0.json: entry book-one") ||
		!hasProblem(res.Warnings, "over the 262144-byte pack target") {
		t.Errorf("expected an advisory for the oversized entry, got:\n%s", joinProblems(res.Warnings))
	}
}

// bigWork returns a valid work record whose description pads it out to roughly
// size bytes.
func bigWork(slug string, size int) string {
	return `{"authors":["author-one"],"description":"` + strings.Repeat("a", size) +
		`","id":` + strconv.Quote(slug) + `,"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"T"}`
}

// manyWorks returns n valid work entries keyed by slug, the first of them
// book-one so the rest of the fixture tree still resolves against it.
func manyWorks(n int) map[string]string {
	out := map[string]string{"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne})}
	for i := 1; i < n; i++ {
		slug := fmt.Sprintf("w%05d", i)
		out[slug] = pkWork(slug)
	}
	return out
}

// manyPacks returns n bound-correct works packs under dir: the first carries
// the reserved minimum bound (and book-one, so the rest of the fixture tree
// still resolves), each later one is named by the entry it holds.
func manyPacks(dir string, n int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("w%05d", i)
		if i == 0 {
			out[dir+"/"+pack.MinBound+".json"] = packOf(map[string]string{
				"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
				slug:       pkWork(slug),
			})
			continue
		}
		out[dir+"/"+slug+".json"] = packOf(map[string]string{slug: pkWork(slug)})
	}
	return out
}

// manySeriesPacks returns n single-entry series packs in the flat series family.
func manySeriesPacks(n int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("s%05d", i)
		bound := slug
		if i == 0 {
			bound = pack.MinBound
		}
		out["series/"+bound+".json"] = packOf(map[string]string{
			slug: `{"id":"` + slug + `","license":"CC0-1.0","name":"S","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`,
		})
	}
	return out
}

// TestPackBoundsWellFormedTree is the passing fixture for the bound rules: a
// multi-directory, multi-pack family whose bounds, directory bounds and entry
// placement all agree loads clean.
func TestPackBoundsWellFormedTree(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
		"cook-one": pkWork("cook-one"),
	})
	files["works/m-book/m-book.json"] = packOf(map[string]string{"m-book": pkWork("m-book")})
	files["works/m-book/p-book.json"] = packOf(map[string]string{"p-book": pkWork("p-book")})
	files["works/z-book/z-book.json"] = packOf(map[string]string{"z-book": pkWork("z-book")})
	writeTree(t, dir, files)

	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a well-formed multi-directory family reported problems:\n%s", joinProblems(res.Problems))
	}
	if len(res.Catalog.Works) != 5 {
		t.Errorf("expected 5 works across the packs, got %d", len(res.Catalog.Works))
	}
}
