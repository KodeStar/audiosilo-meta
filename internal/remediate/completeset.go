package remediate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// completeset.go reads the OPTIONAL dump rows for the complete-set products
// Audible sells alongside the parts ("The Blood Mirror [Dramatized
// Adaptation]", ASIN B09G7MDXSQ). They are what lets a minted work carry a real
// product title and a real identifier instead of a derived one.
//
// The accepted shape is the row `metaimport libex` already parses (see
// scripts/libex-export-rows.sql), so the operator exports them with the
// validated query rather than a second one invented here. Only the factual
// fields this merge decides about are read.

// completeSet is one dump row for a whole-book product.
type completeSet struct {
	ASIN          string          `json:"asin"`
	Title         string          `json:"title"`
	Region        string          `json:"region"`
	Publisher     string          `json:"publisher"`
	ReleaseDate   string          `json:"releaseDate"`
	ImageURL      string          `json:"imageUrl"`
	LengthMinutes int             `json:"lengthMinutes"`
	Authors       []nameRef       `json:"authors"`
	Chapters      json.RawMessage `json:"chapters"`
}

type nameRef struct {
	Name string `json:"name"`
}

// releaseDay is the row's release date as a plain YYYY-MM-DD, empty when the
// row states none. The dump stores an ISO timestamp; the day is the fact.
func (c *completeSet) releaseDay() string {
	if len(c.ReleaseDate) < 10 {
		return ""
	}
	return c.ReleaseDate[:10]
}

// coverURL returns the row's cover, but only over https - the same bar the
// importer holds a cover to.
func (c *completeSet) coverURL() string {
	if strings.HasPrefix(c.ImageURL, "https://") {
		return c.ImageURL
	}
	return ""
}

// authorSlugs renders the row's author names as the person ids the catalogue
// addresses them by, through the same call the importer mints them with.
func (c *completeSet) authorSlugs() []string {
	out := make([]string, 0, len(c.Authors))
	for _, a := range c.Authors {
		slug, _ := model.PersonSlug(a.Name)
		out = append(out, slug)
	}
	return out
}

// chapterList returns the row's chapters translated onto the recording's own
// chapter shape, nil when the row carries none.
func (c *completeSet) chapterList() []model.Chapter {
	var raw []struct {
		Title         string `json:"title"`
		StartOffsetMS int64  `json:"startOffsetMs"`
		LengthMS      int64  `json:"lengthMs"`
	}
	if err := json.Unmarshal(c.Chapters, &raw); err != nil || len(raw) == 0 {
		return nil
	}
	out := make([]model.Chapter, 0, len(raw))
	for _, r := range raw {
		out = append(out, model.Chapter{Title: r.Title, StartMS: r.StartOffsetMS, LengthMS: r.LengthMS})
	}
	return out
}

// loadCompleteSets reads an NDJSON (or JSON-array) file of dump rows. An empty
// path is not an error: the whole enrichment is optional and the merge composes
// a derived title without it.
func loadCompleteSets(path string) ([]*completeSet, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // an operator-supplied export path
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
	var out []*completeSet
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") {
			return nil, fmt.Errorf("%s: expected NDJSON (one row per line), got a JSON array", path)
		}
		var row completeSet
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, &row)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// matchCompleteSets attaches at most one dump row to each group: a row whose
// title is the group's base title plus an optional dramatized marker, with no
// part marker of its own, and whose authors are the group's. Several matching
// rows are an ambiguity, so the group is merged from its parts alone and the
// ambiguity is reported.
func matchCompleteSets(groups []*group, rows []*completeSet) []Refusal {
	byKey := map[string][]*completeSet{}
	for _, r := range rows {
		if _, isPart := partOf(r.Title); isPart {
			continue
		}
		key := groupKey(baseTitle(r.Title), r.authorSlugs())
		byKey[key] = append(byKey[key], r)
	}
	var refusals []Refusal
	for _, g := range groups {
		if g.refused {
			continue
		}
		found := byKey[g.key()]
		switch len(found) {
		case 0:
		case 1:
			if regionAllowed(found[0].Region) {
				g.Set = found[0]
				continue
			}
			refusals = append(refusals, Refusal{
				Category: catAmbiguousSet,
				Subject:  g.Base,
				Reason:   fmt.Sprintf("the complete-set row states region %q, which is not a marketplace this schema knows", found[0].Region),
				Entries:  []string{found[0].ASIN},
			})
		default:
			asins := make([]string, 0, len(found))
			for _, r := range found {
				asins = append(asins, r.ASIN)
			}
			sortStrings(asins)
			refusals = append(refusals, Refusal{
				Category: catAmbiguousSet,
				Subject:  g.Base,
				Reason:   "more than one complete-set row matches this book; merged from the parts alone",
				Entries:  asins,
			})
		}
	}
	return refusals
}

// regions is the marketplace vocabulary, read from the embedded schema rather
// than restated here. One vocabulary, one definition - the drift guard the
// project holds every other mirror of this list to.
var regions = sync.OnceValue(func() map[string]bool {
	raw, err := meta.SchemaFS.ReadFile("schema/common.schema.json")
	if err != nil {
		panic("remediate: reading the embedded common schema: " + err.Error())
	}
	var doc struct {
		Defs struct {
			Region struct {
				Enum []string `json:"enum"`
			} `json:"region"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic("remediate: parsing the embedded common schema: " + err.Error())
	}
	out := make(map[string]bool, len(doc.Defs.Region.Enum))
	for _, r := range doc.Defs.Region.Enum {
		out[r] = true
	}
	if len(out) == 0 {
		panic("remediate: the embedded common schema states no region vocabulary")
	}
	return out
})

// regionAllowed reports whether a dump row's region is one the schema knows.
func regionAllowed(region string) bool { return regions()[strings.ToLower(region)] }
