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
// which it FINDS through this package's own resolution, SeriesAuthorIndex.Resolve).
//
// The rule per family follows what the family's slug MEANS:
//
//   - PEOPLE. The slug is the identity (model.PersonSlug), so a retired person slug
//     resolves to its survivor outright (livePerson), on the creating path
//     (getOrCreatePerson) and every resolving one alike (resolvePerson).
//   - SERIES. Only the chain's FIRST candidate is a statement about the name: a
//     tombstoned base resolves to the survivor, with no name comparison (the
//     tombstone IS the decision, and the survivor's name is usually the other
//     spelling). A tombstoned NUMBERED candidate is treated as OCCUPIED by a
//     different series and stepped past - which name that "-3" once carried is not
//     recorded, and joining the wrong series is worse than minting a new one.
//     A survivor is judged by the row's AUTHORS like every other candidate
//     (seriesauthors.go): a merge said which series the name means, not that
//     every author's book belongs in it. seriesCandidates (seriesresolve.go) is
//     the one chain walk, so the importer, libex-select and the intake form
//     cannot disagree.
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
