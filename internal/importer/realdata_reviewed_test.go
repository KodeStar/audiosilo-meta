package importer

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/raceflag"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// TestDataLibrivoxReviewedDecisionsHold is the decision table's STALE check over the real
// tree: every attach's project sits on its work (through the tombstones, so a later merge
// carries it), and every own-work's project is held somewhere, a project number being
// what a decision is about.
func TestDataLibrivoxReviewedDecisionsHold(t *testing.T) {
	if raceflag.Enabled {
		t.Skip("real-data test: skipped under -race (see CLAUDE.md)")
	}
	reds, err := redirects.Load(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	held := map[int]string{}
	walkEntries(t, "works", func(_, key string, e map[string]any) {
		recs, _ := e["recordings"].(map[string]any)
		for _, r := range recs {
			rec, _ := r.(map[string]any)
			if id, ok := rec["librivox_id"].(float64); ok {
				held[int(id)] = key
			}
		}
	})
	for project, r := range librivoxReviewed() {
		work, ok := held[project]
		if !ok {
			t.Errorf("project %d %q: decided %s, and the catalogue does not hold it", project, r.Title, r.Decision)
			continue
		}
		if r.Decision != LibrivoxReviewAttach {
			continue
		}
		want := r.Work
		if to, retired := reds.Survivor(model.RedirectWorks, want); retired {
			want = to
		}
		if work != want {
			t.Errorf("project %d %q: decided onto %q, and it sits on %q", project, r.Title, want, work)
		}
	}
}

// TestDataLibrivoxOwnWorksLeaveTheGuardArmed measures what the set-apart is for: of the
// own-work homes whose key also answers another work, none may still read as ambiguous.
// The log line is the measurement.
func TestDataLibrivoxOwnWorksLeaveTheGuardArmed(t *testing.T) {
	if raceflag.Enabled {
		t.Skip("real-data test: skipped under -race (see CLAUDE.md)")
	}
	res := check.LoadProfile(dataDir, pack.ProfileCore)
	if res.Catalog == nil || res.Identity == nil {
		t.Fatalf("load: %d problems, no catalogue or index", len(res.Problems))
	}
	homes := LibrivoxOwnWorkHomes(res.Catalog.Works)
	byID := map[string]*model.Work{}
	for _, w := range res.Catalog.Works {
		byID[w.ID] = w
	}
	probe := func(w *model.Work) []check.IdentityMatch {
		identity := ToSet(w.Authors)
		for _, c := range w.Credits {
			delete(identity, c.Person)
		}
		return res.Identity.Match(w.Title, res.Identity.SeriesNameOf(w.ID), w.Language, ToSet(w.Authors), identity)
	}
	collided := map[string]bool{}
	for id := range homes {
		if len(probe(byID[id])) > 1 {
			collided[id] = true
		}
	}
	res.Identity.SetApart(homes)
	armed, stillAmbiguous := 0, 0
	for id := range collided {
		ms := probe(byID[id])
		if len(ms) == 1 && !homes[ms[0].Work.ID] {
			armed++
		}
		others := 0
		for _, m := range ms {
			if !homes[m.Work.ID] {
				others++
			}
		}
		if others > 1 {
			stillAmbiguous++
		}
	}
	t.Logf("%d own-work homes: %d share a key with another work, which would disarm the guard; set apart, %d answer one catalogued work and %d keys still name several non-own works",
		len(homes), len(collided), armed, stillAmbiguous)
}
