package query

import (
	"strings"
	"testing"
)

// TestShardWindowsThePageItPromises checks the OFFSET arithmetic against the
// database rather than only the count: with the cap forced down to 2, shard 0
// and shard 1 partition the works family in id order with no gap and no repeat.
func TestShardWindowsThePageItPromises(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	var got []string
	for shard := range 2 {
		rows, err := snap.db.Query(worksSitemapSQL, 2, shard*2)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			var added *string
			if err := rows.Scan(&id, &added); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(ids) != 2 {
			t.Fatalf("shard %d returned %d ids, want 2", shard, len(ids))
		}
		got = append(got, ids...)
	}
	want := "edgedancer,project-hail-mary,the-way-of-kings,words-of-radiance"
	if strings.Join(got, ",") != want {
		t.Errorf("windowed ids = %v, want %s", got, want)
	}
}
