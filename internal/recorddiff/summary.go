package recorddiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// summary.go renders an ADDED or REMOVED record as one line.
//
// A line is the whole of what a reviewer gets about a new record, so it states
// the facts a judgement is made on - who wrote it, when, in what language, what
// narrations it ships with and where each one's facts came from - and nothing
// about storage. It is deliberately not the record's JSON: at a hundred works a
// tranche, the JSON is the thing that did not fit.
//
// A field the record does not carry renders as "(none)" rather than being left
// out, because an absent first_published or an empty sources list is itself worth
// seeing and a clause that silently disappears cannot be noticed.

// maxSummaryRecordings bounds the per-work recording list. A work with more
// narrations than this is real (a public-domain classic can carry dozens), and
// one line per tranche entry is the budget this whole rendering exists to respect.
const maxSummaryRecordings = 8

// maxSummaryChars bounds one field of a summary line - a title, a name. The cap
// is generous enough that no honest title reaches it and tight enough that a
// record carrying pasted back-cover copy cannot spend the whole budget.
const maxSummaryChars = 200

// addedSummary renders the descriptor of a newly added record, WITHOUT the
// leading "+ " the renderer adds.
func addedSummary(f pack.Family, slug string, raw json.RawMessage) string {
	switch f {
	case pack.FamilyWorks:
		return workSummary(slug, raw)
	case pack.FamilyPeople:
		return personSummary(slug, raw)
	case pack.FamilySeries:
		// A series entry is LOUD on purpose. No bot in this repository mints one -
		// the importer attaches works to a series that already exists - so a new
		// series in a machine-opened tranche is the shape of a mistake, and a
		// reviewer has to see it among a hundred ordinary work lines.
		return seriesSummary(slug, raw)
	case pack.FamilyWorksCommunity:
		return communitySummary(slug, raw)
	}
	return fmt.Sprintf("%s %s", familyWord(f), slug)
}

// removedSummary renders a record that the change deletes. Only its identity and
// its name: what a removal needs justifying is the removal, and the record's
// fields are in git.
func removedSummary(f pack.Family, slug string, raw json.RawMessage) string {
	return fmt.Sprintf("%s %s: %s", familyWord(f), slug, quoted(recordName(raw)))
}

// familyWord is the singular noun a family's records are named by in a rendered
// line. It is the family's own spelling for anything this package does not model.
func familyWord(f pack.Family) string {
	switch f {
	case pack.FamilyWorks:
		return "work"
	case pack.FamilyPeople:
		return "person"
	case pack.FamilySeries:
		return "series"
	case pack.FamilyWorksCommunity:
		return "community"
	}
	return string(f)
}

// familyRank is the order families are PRINTED in, and it is not alphabetical on
// purpose. Truncation drops the tail of a section, so what a reviewer would miss
// least has to sit there: a new SERIES first (no bot in this repository mints
// one, so one in a machine-opened tranche is the loudest thing in it), then the
// works, then the sidecars, and the people last - a new person record is almost
// always the mechanical consequence of a new work's author or narrator.
func familyRank(f pack.Family) int {
	switch f {
	case pack.FamilySeries:
		return 0
	case pack.FamilyWorks:
		return 1
	case pack.FamilyWorksCommunity:
		return 2
	case pack.FamilyPeople:
		return 3
	}
	return 4
}

// familyLess is the ONE ordering every list in a render is sorted by.
func familyLess(a, b pack.Family) bool {
	if ra, rb := familyRank(a), familyRank(b); ra != rb {
		return ra < rb
	}
	return a < b
}

// familyPlural is familyWord for a count of them.
func familyPlural(f pack.Family) string {
	switch f {
	case pack.FamilyWorks:
		return "works"
	case pack.FamilyPeople:
		return "people"
	case pack.FamilySeries:
		return "series"
	case pack.FamilyWorksCommunity:
		return "community entries"
	}
	return string(f)
}

// workView is the subset of a works-family composite entry a summary states.
// Everything else in the entry is carried by the record itself and is not part of
// the judgement a one-line descriptor supports.
type workView struct {
	Title          string             `json:"title"`
	Subtitle       string             `json:"subtitle"`
	Authors        []string           `json:"authors"`
	Language       string             `json:"language"`
	FirstPublished string             `json:"first_published"`
	Genres         []string           `json:"genres"`
	Recordings     map[string]recView `json:"recordings"`
}

// recView is the subset of a recording a summary states.
type recView struct {
	Narrators   []string          `json:"narrators"`
	ASIN        []model.ASIN      `json:"asin"`
	ReleaseDate string            `json:"release_date"`
	Chapters    []json.RawMessage `json:"chapters"`
	Sources     []model.Source    `json:"sources"`
}

// workSummary renders a work composite: the work's own facts, then one bracket
// per recording.
func workSummary(slug string, raw json.RawMessage) string {
	var w workView
	if err := json.Unmarshal(raw, &w); err != nil {
		return fmt.Sprintf("work %s: (entry could not be read: %v)", slug, err)
	}
	title := w.Title
	if w.Subtitle != "" {
		title += ": " + w.Subtitle
	}
	var b strings.Builder
	fmt.Fprintf(&b, "work %s: %s by %s, lang %s, first_published %s, genres %s; recordings: %d",
		slug, quoted(title), list(w.Authors), value(w.Language), value(w.FirstPublished),
		list(w.Genres), len(w.Recordings))
	for i, rec := range sortedKeys(w.Recordings) {
		if i == maxSummaryRecordings {
			fmt.Fprintf(&b, " [+%d more recording(s)]", len(w.Recordings)-maxSummaryRecordings)
			break
		}
		fmt.Fprintf(&b, " [%s]", recordingSummary(rec, w.Recordings[rec]))
	}
	return b.String()
}

// recordingSummary renders one narration's bracket.
func recordingSummary(slug string, r recView) string {
	regions := make([]string, 0, len(r.ASIN))
	for _, a := range r.ASIN {
		if a.Region != "" {
			regions = append(regions, a.Region)
		}
	}
	asins := fmt.Sprintf("%d asin(s)", len(r.ASIN))
	if len(regions) > 0 {
		asins += " (" + list(unique(regions)) + ")"
	}
	types := make([]string, 0, len(r.Sources))
	for _, s := range r.Sources {
		types = append(types, s.Type)
	}
	return fmt.Sprintf("%s: narrators %s, %s, release %s, %d chapters, sources %s",
		slug, list(r.Narrators), asins, value(r.ReleaseDate), len(r.Chapters), list(unique(types)))
}

// personView is the subset of a person record a summary states.
type personView struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// personSummary renders a person. An absent kind is rendered as "person", which
// is what absence MEANS in the schema - not a guess this package is making.
func personSummary(slug string, raw json.RawMessage) string {
	var p personView
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Sprintf("person %s: (entry could not be read: %v)", slug, err)
	}
	kind := p.Kind
	if kind == "" {
		kind = "person"
	}
	return fmt.Sprintf("person %s: %s kind %s", slug, quoted(p.Name), kind)
}

// seriesView is the subset of a series record a summary states.
type seriesView struct {
	Name  string `json:"name"`
	Works []struct {
		Work string `json:"work"`
	} `json:"works"`
}

// seriesSummary renders a series in SHOUTING case - see addedSummary.
func seriesSummary(slug string, raw json.RawMessage) string {
	var s seriesView
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Sprintf("SERIES %s: (entry could not be read: %v)", slug, err)
	}
	noun := "works"
	if len(s.Works) == 1 {
		noun = "work"
	}
	return fmt.Sprintf("SERIES %s: %s (%d %s)", slug, quoted(s.Name), len(s.Works), noun)
}

// communitySummary renders a works-community entry by the members it carries.
// This repository holds no such family (the CC BY-SA layer is its own repo), so
// the case exists for a run pointed at the community tree.
func communitySummary(slug string, raw json.RawMessage) string {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Sprintf("community %s: (entry could not be read: %v)", slug, err)
	}
	names := make([]string, 0, len(members))
	for k := range members {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Sprintf("community %s: members %s", slug, list(names))
}

// recordName pulls whatever a record calls itself: a work's title, a person's or
// series' name.
func recordName(raw json.RawMessage) string {
	var v struct {
		Title string `json:"title"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	if v.Title != "" {
		return v.Title
	}
	return v.Name
}

// quoted renders a name as a quoted, length-capped string; an empty one as
// "(none)" rather than as a pair of empty quotes.
func quoted(s string) string {
	if s == "" {
		return "(none)"
	}
	return `"` + clip(s, maxSummaryChars) + `"`
}

// value renders a bare scalar field, empty as "(none)".
func value(s string) string {
	if s == "" {
		return "(none)"
	}
	return clip(s, maxSummaryChars)
}

// list renders a slug list, empty as "(none)".
func list(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return clip(strings.Join(items, ", "), maxSummaryChars)
}

// unique returns the distinct values of items, sorted, so one recording's regions
// and source types render the same however the record ordered them.
func unique(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns a map's keys in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// clip shortens s to at most n characters, marking that it did. It counts RUNES,
// so a cut never lands inside a multi-byte character and produces the invalid
// UTF-8 that the shell wrapper's jq --arg would refuse.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
