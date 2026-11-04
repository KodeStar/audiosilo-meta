package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// narrationcheck.go is the MERGED-NARRATION check (`metaimport narrations`): a
// recording whose ASINs are two productions - The Martian's r-c-bray-2020 held Wil
// Wheaton's B082BHJMFF (Audible Studios, 2020) beside R.C. Bray's B00B5HZGUG
// (Podium, 2013), because a library import attached the second ASIN to the first
// recording of the same title. A server that matched either listing was then told
// the wrong narrator, which the LibraForge benchmark (2026-11-04) scored as
// "another recording".
//
// The catalogue cannot answer this on its own: an asin[] entry stores only the
// identifier and its marketplace, never the narrators or runtime of the product it
// names, so pkg/check has nothing to compare. What the stored facts CAN do is pick
// the suspects - a recording holding two or more distinct ASINs in ONE marketplace
// (a production is sold once per marketplace, so a second identifier there is a
// re-release or another production) - and the check then asks libex what each
// ASIN is, through the same client and polite pacing libex-fill uses.
//
// It is READ-ONLY on the tree. A finding is a worklist line for a reviewer: two
// listings of one narration (a re-release, a retailer duplicate) are common and
// harmless, so only a DISJOINT narrator set is reported as two productions, and
// nothing is split by this code. The libex rows it fetched for a finding can be
// written beside the worklist, which is what a reviewer feeds `metaimport libex
// --recordings-only` once the wrong ASIN is removed from the merged recording.

// NarrationSuspect is one recording the stored facts single out.
type NarrationSuspect struct {
	Work      string
	Recording string
	// ASINs are the recording's distinct identifiers, in stored order.
	ASINs     []string
	Narrators []string
}

// SelectNarrationSuspects returns the recordings holding two or more distinct
// ASINs in one marketplace, sorted by (work, recording). works, when non-empty,
// restricts the selection to those work ids and then takes every ASIN-bearing
// recording of them, suspect or not - a reviewer naming a work wants it checked.
func SelectNarrationSuspects(cat *model.Catalog, works map[string]bool) []NarrationSuspect {
	// Only individuals are compared: a group, publisher or synthetic credit (a
	// full cast, Audible Studios) is spelled a dozen ways by retailers.
	notIndividual := map[string]bool{}
	for _, p := range cat.People {
		if p.Kind != "" && p.Kind != model.KindEntityPerson {
			notIndividual[p.ID] = true
		}
	}
	var out []NarrationSuspect
	for _, w := range cat.Works {
		if len(works) > 0 && !works[w.ID] {
			continue
		}
		for _, r := range w.Recordings {
			perRegion := map[string]map[string]bool{}
			var distinct []string
			seen := map[string]bool{}
			for _, a := range r.ASIN {
				if a.ASIN == "" {
					continue
				}
				if perRegion[a.Region] == nil {
					perRegion[a.Region] = map[string]bool{}
				}
				perRegion[a.Region][a.ASIN] = true
				if !seen[a.ASIN] {
					seen[a.ASIN] = true
					distinct = append(distinct, a.ASIN)
				}
			}
			suspect := len(works) > 0 && len(distinct) > 0
			for _, set := range perRegion {
				if len(set) > 1 {
					suspect = true
				}
			}
			if suspect {
				var individuals []string
				for _, n := range r.Narrators {
					if !notIndividual[n] && !castCredit(n) {
						individuals = append(individuals, n)
					}
				}
				out = append(out, NarrationSuspect{Work: w.ID, Recording: r.ID, ASINs: distinct, Narrators: individuals})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Work != out[j].Work {
			return out[i].Work < out[j].Work
		}
		return out[i].Recording < out[j].Recording
	})
	return out
}

// UserLibraryNarrations keeps the suspects whose recording carries a USER-LIBRARY
// source (model.TierUserLibrary). That is where the merge happens: a library
// import that meets a held recording of the same title adds its ASIN to it, so a
// second production lands there, while a libex-seeded recording's second ASIN is
// nearly always the same production listed twice. It bounds a whole-catalogue run
// to the population worth the lookups.
func UserLibraryNarrations(suspects []NarrationSuspect, cat *model.Catalog) []NarrationSuspect {
	tier := map[string]bool{}
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			for _, src := range r.Sources {
				if model.TierOfSource(src.Type) == model.TierUserLibrary {
					tier[w.ID+"\x00"+r.ID] = true
				}
			}
		}
	}
	out := suspects[:0:0]
	for _, s := range suspects {
		if tier[s.Work+"\x00"+s.Recording] {
			out = append(out, s)
		}
	}
	return out
}

// The finding kinds.
const (
	// NarrationTwoProductions: the recording's ASINs fall into two or more groups
	// whose narrator sets share nobody.
	NarrationTwoProductions = "asins-name-different-narrators"
	// NarrationStoredDisagrees: every ASIN agrees on the narrators, and the
	// recording credits none of them.
	NarrationStoredDisagrees = "recording-credits-other-narrators"
)

// NarrationGroup is the ASINs libex credits one narrator set for.
type NarrationGroup struct {
	ASINs     []string `json:"asins"`
	Narrators []string `json:"narrators"`
	// RuntimeMin is each ASIN's libex runtime, in ASINs order (0 = unstated).
	RuntimeMin []int `json:"runtime_min"`
	// Matches is whether this group shares a narrator with the recording.
	Matches bool `json:"matches_recording"`
}

// NarrationFinding is one worklist line.
type NarrationFinding struct {
	Kind      string           `json:"kind"`
	Work      string           `json:"work"`
	Recording string           `json:"recording"`
	Narrators []string         `json:"recording_narrators"`
	Groups    []NarrationGroup `json:"groups"`
	// Unchecked are the ASINs libex did not answer for.
	Unchecked []string `json:"unchecked,omitempty"`
}

// NarrationReport counts a run.
type NarrationReport struct {
	Recordings int
	Lookups    int
	NotFound   int
	Failed     int
	Findings   int
	Errors     []string
}

// narratorKey folds a narrator credit (a libex name or a stored person id) so the
// two spellings meet: the person slug with its separators and its middle initials
// dropped, which folds an initials spelling ("R.C. Bray" / r-c-bray) and a stated
// or omitted middle initial ("Richard M Davidson" / richard-davidson).
func narratorKey(s string) string {
	slug, _ := model.PersonSlug(s)
	words := strings.Split(slug, "-")
	kept := words[:0:0]
	for i, w := range words {
		if len(w) == 1 && i > 0 && i < len(words)-1 {
			continue
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, "")
}

// splitCredit splits one libex credit naming two people ("Kate Reading and Michael
// Kramer") into its names. Only a join of two names of two words or more is split,
// so a single name holding "and" stays whole.
func splitCredit(s string) []string {
	for _, sep := range []string{" and ", " & "} {
		if a, b, ok := strings.Cut(s, sep); ok {
			a, b = strings.TrimSpace(a), strings.TrimSpace(b)
			if len(strings.Fields(a)) >= 2 && len(strings.Fields(b)) >= 2 {
				return []string{a, b}
			}
		}
	}
	return []string{s}
}

// CheckNarrations looks every suspect's ASINs up on libex and returns the
// findings. rows, when non-nil, receives the export-shaped row of every ASIN in a
// finding (the input `metaimport libex` reads). A lookup error is counted and the
// ASIN left unchecked; it never fails the run.
func (c *LibexClient) CheckNarrations(ctx context.Context, suspects []NarrationSuspect, rows io.Writer) ([]NarrationFinding, NarrationReport, error) {
	rep := NarrationReport{Recordings: len(suspects)}
	var enc *json.Encoder
	if rows != nil {
		enc = json.NewEncoder(rows)
	}
	first := true
	var out []NarrationFinding
	for _, s := range suspects {
		var got []lookedASIN
		var unchecked []string
		for _, asin := range s.ASINs {
			if err := ctx.Err(); err != nil {
				return out, rep, err
			}
			if !first && c.Pause > 0 {
				select {
				case <-ctx.Done():
					return out, rep, ctx.Err()
				case <-time.After(c.Pause):
				}
			}
			first = false
			rep.Lookups++
			rec, err := c.fetchOne(ctx, asin)
			switch {
			case errors.Is(err, errLibexNotFound):
				rep.NotFound++
				unchecked = append(unchecked, asin)
				continue
			case err != nil:
				rep.Failed++
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", asin, err))
				unchecked = append(unchecked, asin)
				continue
			}
			l := lookedASIN{asin: asin, row: libexFillRow(rec)}
			for _, n := range libexNames(rec["narrators"]) {
				if !castCredit(n) {
					l.narrators = append(l.narrators, splitCredit(n)...)
				}
			}
			if m, ok := coerceInt(rec["lengthMinutes"]); ok {
				l.runtime = int(m)
			}
			got = append(got, l)
		}
		f, ok := judgeNarration(s, got)
		if !ok {
			continue
		}
		f.Unchecked = unchecked
		out = append(out, f)
		rep.Findings++
		if enc != nil {
			for _, g := range got {
				if err := enc.Encode(g.row); err != nil {
					return out, rep, err
				}
			}
		}
	}
	return out, rep, nil
}

// lookedASIN is what libex said about one ASIN, and its export-shaped row.
type lookedASIN struct {
	asin      string
	narrators []string
	runtime   int
	row       map[string]any
}

// judgeNarration groups a suspect's answered ASINs by narrator set and decides
// whether they are a finding. An ASIN libex states no narrator for joins no group
// and decides nothing. Groups are joined transitively by any shared narrator, so a
// re-release that added a co-narrator stays one production.
func judgeNarration(s NarrationSuspect, ls []lookedASIN) (NarrationFinding, bool) {
	stored := map[string]bool{}
	for _, n := range s.Narrators {
		stored[narratorKey(n)] = true
	}
	type group struct {
		keys map[string]bool
		g    NarrationGroup
	}
	// Every answered ASIN starts as its own group, in ASIN order.
	var groups []*group
	for _, l := range ls {
		if len(l.narrators) == 0 {
			continue
		}
		g := &group{keys: map[string]bool{}, g: NarrationGroup{ASINs: []string{l.asin}, RuntimeMin: []int{l.runtime}}}
		for _, n := range l.narrators {
			g.keys[narratorKey(n)] = true
			if !slices.Contains(g.g.Narrators, n) {
				g.g.Narrators = append(g.g.Narrators, n)
			}
		}
		groups = append(groups, g)
	}
	// Fold any two groups sharing a narrator into the earlier one until stable, so a
	// later ASIN bridges two groups formed before it.
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(groups) && !merged; i++ {
			for j := i + 1; j < len(groups) && !merged; j++ {
				if !sharesKey(groups[i].keys, groups[j].keys) {
					continue
				}
				dst, src := groups[i], groups[j]
				for k := range src.keys {
					dst.keys[k] = true
				}
				dst.g.ASINs = append(dst.g.ASINs, src.g.ASINs...)
				dst.g.RuntimeMin = append(dst.g.RuntimeMin, src.g.RuntimeMin...)
				for _, n := range src.g.Narrators {
					if !slices.Contains(dst.g.Narrators, n) {
						dst.g.Narrators = append(dst.g.Narrators, n)
					}
				}
				groups = slices.Delete(groups, j, j+1)
				merged = true
			}
		}
	}
	f := NarrationFinding{Work: s.Work, Recording: s.Recording, Narrators: s.Narrators}
	anyMatch := false
	for _, g := range groups {
		g.g.Matches = sharesKey(g.keys, stored)
		anyMatch = anyMatch || g.g.Matches
		f.Groups = append(f.Groups, g.g)
	}
	switch {
	case len(groups) > 1:
		f.Kind = NarrationTwoProductions
	case len(groups) == 1 && len(stored) > 0 && !anyMatch:
		f.Kind = NarrationStoredDisagrees
	default:
		return NarrationFinding{}, false
	}
	return f, true
}

// castCredit reports a group credit spelled as a name ("Full Cast", "An Ensemble
// Cast", full-cast), which names no individual to compare.
func castCredit(s string) bool {
	slug, _ := model.PersonSlug(s)
	for _, w := range strings.Split(slug, "-") {
		if w == "cast" {
			return true
		}
	}
	return false
}

func sharesKey(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}
