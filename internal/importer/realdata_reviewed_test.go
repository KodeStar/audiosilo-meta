package importer

import (
	"cmp"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/raceflag"
	"github.com/kodestar/audiosilo-meta/pkg/model"
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
	titles := map[int]string{}
	walkEntries(t, "works", func(_, key string, e map[string]any) {
		recs, _ := e["recordings"].(map[string]any)
		for _, r := range recs {
			rec, _ := r.(map[string]any)
			if id, ok := rec["librivox_id"].(float64); ok {
				held[int(id)] = key
				titles[int(id)] = cmp.Or(str(rec, "librivox_title"), str(e, "title"))
			}
		}
	})
	for project, r := range librivoxReviewed() {
		work, ok := held[project]
		if !ok {
			t.Errorf("project %d %q: decided %s, and the catalogue does not hold it", project, r.Title, r.Decision)
			continue
		}
		if !reviewedTitleHolds(r, titles[project]) {
			t.Errorf("project %d: decided as %q, and the catalogue holds it as %q", project, r.Title, titles[project])
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
