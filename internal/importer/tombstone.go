package importer

import (
	"fmt"
	"sort"
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
// removed. Minting there anyway was never silent (metacheck refuses a tombstone
// whose source is a live id), but it failed the WHOLE run, and every repair wave
// adds tombstones: issue #2320 was a personal library import stopped by one series
// name the previous wave had retired.
//
// The rule per family follows what the family's slug MEANS:
//
//   - PEOPLE. The slug is the identity (model.PersonSlug), so a retired person slug
//     resolves to its survivor outright, on the creating path (getOrCreatePerson)
//     and the resolving one (personSlugTarget) alike.
//   - SERIES. Only the chain's FIRST candidate is a statement about the name: a
//     tombstoned base resolves to the survivor, with no name comparison (the
//     tombstone IS the decision, and the survivor's name is usually the other
//     spelling). A tombstoned NUMBERED candidate is treated as OCCUPIED by a
//     different series and stepped past - which name that "-3" once carried is not
//     recorded, and joining the wrong series is worse than minting a new one.
//     seriesChain is the one walker, so getOrCreateSeries, findSeries and
//     libex-select's seriesIndex.find cannot disagree.
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

// retired returns the survivor a tombstoned slug names, or false.
func (p *planner) retired(kind model.RedirectKind, slug string) (string, bool) {
	to := p.redirects[kind][slug]
	if to == "" || to == slug {
		return "", false
	}
	return to, true
}

// noteTombstone records that a slug the row spelled was resolved onto its survivor.
func (p *planner) noteTombstone(kind model.RedirectKind, from, to string) {
	if p.tombstoneRides == nil {
		p.tombstoneRides = map[string]bool{}
	}
	p.tombstoneRides[fmt.Sprintf("%s %s -> %s", kind, from, to)] = true
}

// reportTombstoneRides appends the run's one note naming every retired slug it
// resolved onto a survivor, sorted so two runs over one input read the same.
func (p *planner) reportTombstoneRides() {
	if len(p.tombstoneRides) == 0 {
		return
	}
	lines := make([]string, 0, len(p.tombstoneRides))
	for l := range p.tombstoneRides {
		lines = append(lines, l)
	}
	sort.Strings(lines)
	p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
		"%d retired slug(s) resolved onto their survivors through %s (a merge retired them, so nothing is re-created there): %s",
		len(lines), pack.RedirectsFile, strings.Join(lines, ", ")))
}

// retiredPerson resolves a person slug the table retires onto the survivor record,
// which must be one the planner knows; ok is false otherwise. It notes nothing:
// the read-only resolvers ask it too, for rows that may never be imported, so
// the ride is recorded where a credit is actually taken (getOrCreatePerson).
func (p *planner) retiredPerson(slug string) (string, bool) {
	to, ok := p.retired(model.RedirectPeople, slug)
	if !ok {
		return "", false
	}
	if _, known := p.people[to]; !known {
		return "", false
	}
	return to, true
}

// workAt answers what a work-slug candidate addresses: the live record, or - when
// the table retires the slug - the survivor, reported through via. occupied is
// true for both, and for a tombstone whose survivor the load did not hold (ws is
// then nil): a minter may never claim a retired slug, whatever it resolves to.
func (p *planner) workAt(slug string) (ws *workState, via string, occupied bool) {
	if ws, live := p.works[slug]; live {
		return ws, "", true
	}
	to, retired := p.retired(model.RedirectWorks, slug)
	if !retired {
		return nil, "", false
	}
	return p.works[to], slug, true
}

// seriesChainAnswer is what a walk of a series name's candidate chain concluded.
type seriesChainAnswer struct {
	// slug is the series the name resolves to when found, else the first slug a
	// new series for the name may be minted at.
	slug  string
	found bool
	// via is the retired base slug the answer was reached through, or "".
	via string
}

// seriesChain walks a series name's candidate chain (SeriesSlugAt over base, the
// name's Slugify) and is the ONE walker behind getOrCreateSeries and its read-only
// twins findSeries and seriesIndex.find. stored reports the name of the series a
// slug holds; retired is the table's series namespace.
//
// A held slug answers when its stored name matches case-insensitively and is
// stepped past otherwise, as it always was. A tombstoned slug answers ONLY at
// index 0 and only when stored holds its survivor; everywhere else it is occupied.
// Counting it occupied keeps the walkers' invariant - the first FREE slug ends the
// walk, because nothing beyond it can have been minted - true of a chain with a
// retired "-2" in it, where stopping there would miss a live "-3".
func seriesChain(base, name string, retired map[string]string, stored func(slug string) (string, bool)) seriesChainAnswer {
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if held, exists := stored(slug); exists {
			if strings.EqualFold(held, name) {
				return seriesChainAnswer{slug: slug, found: true}
			}
			continue
		}
		to := retired[slug]
		if to == "" || to == slug {
			return seriesChainAnswer{slug: slug}
		}
		if i == 0 {
			if _, live := stored(to); live {
				return seriesChainAnswer{slug: to, found: true, via: slug}
			}
		}
	}
}
