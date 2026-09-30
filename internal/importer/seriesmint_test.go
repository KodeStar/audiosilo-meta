package importer

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// FreeSeriesSlug walks the importer's own chain: a held slug and a retired one (base or
// numbered) are both stepped past, a reserved base shifts the chain, and a name that
// slugs away to nothing has no slug at all.
func TestFreeSeriesSlugWalksTheSeriesChain(t *testing.T) {
	held := map[string]bool{"alien": true, "alien-2": true}
	taken := func(s string) bool { return held[s] }
	r := model.NewRedirects()
	r[model.RedirectSeries]["alien-3"] = "alien"

	for _, tc := range []struct {
		name, want string
	}{
		{"Alien", "alien-4"},
		{"Aliens", "aliens"},
		{"Latest", "latest-2"},
		{"Зона", ""},
	} {
		if got := FreeSeriesSlug(tc.name, taken, r); got != tc.want {
			t.Errorf("FreeSeriesSlug(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A retired BASE is stepped past too: the split mints a new series, and a tombstone
	// is a decision that name means the survivor.
	r[model.RedirectSeries]["aliens"] = "alien"
	if got := FreeSeriesSlug("Aliens", taken, r); got != "aliens-2" {
		t.Errorf("retired base: got %q, want aliens-2", got)
	}
	if got := FreeSeriesSlug("Alien", taken, nil); got != "alien-3" {
		t.Errorf("nil table: got %q, want alien-3", got)
	}
}
