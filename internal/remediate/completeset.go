package remediate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// completeset.go reads the OPTIONAL dump rows for the complete-set products
// Audible sells alongside the parts ("Black Prism [Dramatized Adaptation]",
// ASIN B09FRBS927). They are what lets a minted work carry a real product
// title, identifier, length and chapter list instead of a derived one.
//
// The accepted shape is the row `metaimport libex` already parses (see
// scripts/libex-export-rows.sql), so the operator exports them with the
// validated query rather than a second one invented here - and every field is
// normalized through internal/importer's own rules rather than through a second
// copy of them. That matters more than it looks: the dump spells the UK
// marketplace "gb", so a private region check refuses every UK row; a release
// date arrives as a full timestamp only the importer's parser reduces
// correctly; and a chapter list has to pass the same monotonic-from-zero rule
// metacheck enforces before it can be written into a recording.

// completeSet is one dump row for a whole-book product, already normalized.
// Parsing is where a row is judged, so by the time the planner sees one its
// region is a marketplace the schema accepts and its ASIN is well-formed.
type completeSet struct {
	ASIN          string
	Title         string
	Region        string
	Publisher     string
	ReleaseDate   string
	CoverURL      string
	LengthMinutes int
	Authors       []string
	Chapters      []model.Chapter
}

// completeSetRow is the row as the export writes it.
type completeSetRow struct {
	ASIN          string    `json:"asin"`
	Title         string    `json:"title"`
	Region        string    `json:"region"`
	Publisher     string    `json:"publisher"`
	ReleaseDate   string    `json:"releaseDate"`
	ImageURL      string    `json:"imageUrl"`
	LengthMinutes int       `json:"lengthMinutes"`
	Authors       []nameRef `json:"authors"`
	Chapters      any       `json:"chapters"`
}

type nameRef struct {
	Name string `json:"name"`
}

// rowProblem is something the parser declined or dropped, named so the report
// can say which row and why rather than silently reading fewer rows than the
// operator exported.
type rowProblem struct {
	Line   int
	ASIN   string
	Reason string
}

// normalize turns a raw row into a usable one. ok is false for a row this merge
// must not draw on: an identifier that is not an ASIN, or a marketplace the
// schema has no name for - either would otherwise be written verbatim into a
// record and fail validation.
func (r completeSetRow) normalize(warn func(string, ...any)) (*completeSet, string, bool) {
	asin := importer.NormalizeASIN(r.ASIN)
	if asin == "" {
		return nil, fmt.Sprintf("%q is not a well-formed ASIN", r.ASIN), false
	}
	region, ok := importer.MapRegion(r.Region)
	if !ok {
		return nil, fmt.Sprintf("region %q is not a marketplace this schema knows", r.Region), false
	}
	cs := &completeSet{
		ASIN:          asin,
		Title:         strings.TrimSpace(r.Title),
		Region:        region,
		Publisher:     strings.TrimSpace(r.Publisher),
		ReleaseDate:   importer.ISODatePart(r.ReleaseDate),
		LengthMinutes: r.LengthMinutes,
		Chapters:      importer.Chapters(r.Chapters, warn),
	}
	// The schema requires an https cover, the same bar internal/importer holds
	// one to: an http URL cannot be recorded, so it is dropped and said so.
	if img := strings.TrimSpace(r.ImageURL); img != "" {
		if strings.HasPrefix(img, "https://") {
			cs.CoverURL = img
		} else {
			warn("cover URL %q is not https; dropped", img)
		}
	}
	for _, a := range r.Authors {
		slug, _ := model.PersonSlug(a.Name)
		cs.Authors = append(cs.Authors, slug)
	}
	return cs, "", true
}

// loadCompleteSets reads an NDJSON file of dump rows, normalizing each. An
// empty path is not an error: the whole enrichment is optional and the merge
// composes a derived title without it. A row the parser declines, and a field
// it drops, are reported - never silently skipped.
func loadCompleteSets(path string) ([]*completeSet, []rowProblem, error) {
	if path == "" {
		return nil, nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // an operator-supplied export path
	if err != nil {
		return nil, nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
	var out []*completeSet
	var problems []rowProblem
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") {
			return nil, nil, fmt.Errorf("%s: expected NDJSON (one row per line), got a JSON array", path)
		}
		var row completeSetRow
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		at := line
		note := func(format string, a ...any) {
			problems = append(problems, rowProblem{Line: at, ASIN: row.ASIN, Reason: fmt.Sprintf(format, a...)})
		}
		cs, reason, ok := row.normalize(note)
		if !ok {
			note("%s", reason)
			continue
		}
		out = append(out, cs)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, problems, nil
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
		key := groupKey(baseTitle(r.Title), r.Authors)
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
			g.Set = found[0]
		default:
			asins := make([]string, 0, len(found))
			for _, r := range found {
				asins = append(asins, r.ASIN)
			}
			slices.Sort(asins)
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
