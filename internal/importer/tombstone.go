package importer

import (
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// tombstone.go is the MINTERS' side of the slug tombstone table (model.Redirects,
// data/redirects.json): no writer here ever creates a record at a retired slug.
//
// A tombstone is a recorded human decision - a repair wave merged two records and
// said the retired slug names the survivor - so a name that slugs onto one is a
// name for the SURVIVOR, not an invitation to re-create the duplicate the merge
// removed - and minting there fails metacheck's live-source rule for the WHOLE run.
// Every lookup goes through model.Redirects.Survivor. internal/issueform applies
// the same rule at the intake door (one candidate per family, except a series,
// which it FINDS through this package's own chain walk, ResolveSeries).
//
// The rule per family follows what the family's slug MEANS:
//
//   - PEOPLE. The slug is the identity (model.PersonSlug), so a retired person slug
//     resolves to its survivor outright (livePerson), on the creating path
//     (getOrCreatePerson) and the resolving one (personSlugTarget) alike.
//   - SERIES. Only the chain's FIRST candidate is a statement about the name: a
//     tombstoned base resolves to the survivor, with no name comparison (the
//     tombstone IS the decision, and the survivor's name is usually the other
//     spelling). A tombstoned NUMBERED candidate is treated as OCCUPIED by a
//     different series and stepped past - which name that "-3" once carried is not
//     recorded, and joining the wrong series is worse than minting a new one.
//     A survivor is judged by the row's AUTHORS like every other candidate
//     (seriesauthors.go): a merge said which series the name means, not that
//     every author's book belongs in it. seriesChain is the one walker, so
//     getOrCreateSeries, findSeries, libex-select's seriesIndex.find and the
//     intake form (ResolveSeries) cannot disagree.
//   - WORKS. A tombstoned candidate is judged as the SURVIVOR: the row merges into
//     it exactly when the importer's identity rules would merge the row into a live
//     record at that candidate (authors, language, series claim, position suffix),
//     and otherwise the candidate is occupied and the walk steps past it. See
//     workAt.
//
// A tombstone whose target the load did not hold is a red tree (pkg/check refuses
// it, and so does the run's post-write validation) and is never resolved through;
// the series and work chains still count its source occupied.
//
// Every ride is reported, once, in the run's Notes (reportTombstoneRides): a
// record composed under a slug the row did not spell is a decision a reader of the
// pull request has to be able to see.

// noteTombstone records that a slug the row spelled was resolved onto its
// survivor, keyed like the credit merges so the run reports each ride once.
func (p *planner) noteTombstone(kind model.RedirectKind, from, to string) {
	p.tombstoneRides = noteMerge(p.tombstoneRides, string(kind)+" "+from, to)
}

// reportTombstoneRides appends the run's one note naming every retired slug it
// resolved onto a survivor, sorted so two runs over one input read the same.
func (p *planner) reportTombstoneRides() {
	lines := mergeLines(p.tombstoneRides)
	if len(lines) == 0 {
		return
	}
	p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
		"%d retired slug(s) resolved onto their survivors through %s (a merge retired them, so nothing is re-created there): %s",
		len(lines), pack.RedirectsFile, strings.Join(lines, ", ")))
}

// livePerson resolves a person slug onto the record that holds it: the slug
// itself when the planner knows it, else the survivor a tombstone names when the
// planner knows THAT; ok is false otherwise. It notes nothing - the read-only
// resolvers ask it too, for rows that may never be imported - so the ride is
// recorded where a credit is actually taken (getOrCreatePerson).
func (p *planner) livePerson(slug string) (string, bool) {
	if _, known := p.people[slug]; known {
		return slug, true
	}
	if to, retired := p.redirects.Survivor(model.RedirectPeople, slug); retired {
		if _, known := p.people[to]; known {
			return to, true
		}
	}
	return "", false
}

// workAt answers what a work-slug candidate addresses: the live record, or - when
// the table retires the slug - the survivor, reported through via. The candidate
// is OCCUPIED when either is set (ws != nil || via != ""); a tombstone whose
// survivor the load did not hold comes back as a via with a nil ws, because a
// minter may never claim a retired slug, whatever it resolves to.
func (p *planner) workAt(slug string) (ws *workState, via string) {
	if ws, live := p.works[slug]; live {
		return ws, ""
	}
	if to, retired := p.redirects.Survivor(model.RedirectWorks, slug); retired {
		return p.works[to], slug
	}
	return nil, ""
}

// seriesChainAnswer is what a walk of a series name's candidate chain concluded.
type seriesChainAnswer struct {
	// slug is the series the name resolves to when found, else the first slug a
	// new series for the name may be minted at.
	slug  string
	found bool
	// via is the retired base slug the answer was reached through, or "".
	via string
	// stepped are the same-named series the walk stepped past because they did
	// not fit the row's authors (seriesauthors.go), in chain order.
	stepped []string
}

// SeriesMatch is ResolveSeries' answer.
type SeriesMatch struct {
	// Slug is the series the name resolves to when Found, else the first free
	// slug on the chain, where a new series for the name may be minted.
	Slug  string
	Found bool
	// Via is the retired base slug the answer was reached through, or "".
	Via string
	// Stepped are the same-named series the walk stepped past because they did
	// not fit the row's authors, in chain order.
	Stepped []string
}

// ResolveSeries is seriesChain's read-only answer for a writer outside this
// package (internal/issueform). stored reports the name a slug holds; fit judges
// a same-named series for the row naming it (nil admits every one - the
// name-only walk). A name with no addressable slug answers the zero SeriesMatch.
func ResolveSeries(name string, reds model.Redirects, stored func(slug string) (string, bool), fit func(slug string) SeriesFit) SeriesMatch {
	base := Slugify(name)
	if base == "" {
		return SeriesMatch{}
	}
	ans := seriesChain(base, name, reds, stored, fit)
	return SeriesMatch{Slug: ans.slug, Found: ans.found, Via: ans.via, Stepped: ans.stepped}
}

// seriesChain walks a series name's candidate chain (SeriesSlugAt over base, the
// name's Slugify) and is the ONE walker behind getOrCreateSeries and its read-only
// twins findSeries and seriesIndex.find. stored reports the name of the series a
// slug holds; reds is the tombstone table; fit judges a same-named series for the
// row naming it (seriesauthors.go), nil counting every one SeriesShared.
//
// A held slug is a CANDIDATE when its stored name matches case-insensitively and
// is stepped past otherwise, as it always was. A tombstoned slug is a candidate
// ONLY at index 0 and only when stored holds its survivor; everywhere else it is
// occupied. A candidate the row SHARES an author with answers at once; failing
// one, the FIRST OPEN candidate answers when the walk reaches a free slug; a
// CLOSED one is stepped past exactly like a differently-named holder. Counting
// every stepped slug occupied keeps the walkers' invariant - the first FREE slug
// ends the walk, because nothing beyond it can have been minted - true of a chain
// with a retired "-2", or another author's "-2", in it, where stopping there
// would miss a live "-3".
func seriesChain(base, name string, reds model.Redirects, stored func(slug string) (string, bool), fit func(slug string) SeriesFit) seriesChainAnswer {
	var open *seriesChainAnswer
	var stepped []string
	// shared reports whether candidate ans answers the walk now, recording it as
	// the open fallback or as stepped otherwise.
	shared := func(ans seriesChainAnswer) bool {
		f := SeriesShared
		if fit != nil {
			f = fit(ans.slug)
		}
		switch f {
		case SeriesShared:
			return true
		case SeriesOpen:
			if open == nil {
				open = &ans
			}
		default:
			stepped = append(stepped, ans.slug)
		}
		return false
	}
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if held, exists := stored(slug); exists {
			if strings.EqualFold(held, name) {
				if ans := (seriesChainAnswer{slug: slug, found: true}); shared(ans) {
					return ans
				}
			}
			continue
		}
		to, retired := reds.Survivor(model.RedirectSeries, slug)
		if !retired {
			if open != nil {
				return *open
			}
			return seriesChainAnswer{slug: slug, stepped: stepped}
		}
		if i == 0 {
			if _, live := stored(to); live {
				if ans := (seriesChainAnswer{slug: to, found: true, via: slug}); shared(ans) {
					return ans
				}
			}
		}
	}
}
