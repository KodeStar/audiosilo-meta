package query

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

// TestShardWindowsThePageItPromises checks the OFFSET arithmetic against the
// database rather than only the count: with the cap forced down to 2, shard 0
// and shard 1 partition the works family in id order with no gap and no repeat.
func TestShardWindowsThePageItPromises(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, artifacttest.Fixture()))
	var got []string
	for shard := range 2 {
		pages, err := snap.Pages(WorkPages, 2, shard*2)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("shard %d returned %d ids, want 2", shard, len(pages))
		}
		for _, p := range pages {
			got = append(got, p.ID)
		}
	}
	want := "edgedancer,project-hail-mary,the-way-of-kings,words-of-radiance"
	if strings.Join(got, ",") != want {
		t.Errorf("windowed ids = %v, want %s", got, want)
	}
}
