package importer

import "github.com/kodestar/audiosilo-meta/pkg/model"

// FreeSeriesSlug is the slug a NEW series named name is minted at when it is minted
// ALONE rather than by an import batch: the first candidate on the series chain
// (SeriesSlugAt over Slugify(name), so a reserved route literal is stepped off exactly
// as the importer steps off it) that no series holds (taken reports a held slug) and
// the tombstone table does not retire - a retired candidate is stepped past, never
// re-created, whether it is the base or a numbered one (tombstone.go). It returns ""
// for a name that slugs away to nothing, which has no identity to mint and which
// getOrCreateSeries refuses as a claim.
//
// It is mintSlug's rule for a caller outside the planner: internal/repair's
// split-series mints the half of a mixed-language series that moves out under the
// same name, and internal/audit names the slug it would get. taken is the caller's
// view of the family, so a repair run that minted one series already sees that slug
// as held and the next split of the same name walks one step further - the batch
// twin of that is mintSlug's allocated set.
func FreeSeriesSlug(name string, taken func(slug string) bool, r model.Redirects) string {
	base := Slugify(name)
	if base == "" {
		return ""
	}
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if taken(slug) {
			continue
		}
		if _, retired := r.Survivor(model.RedirectSeries, slug); retired {
			continue
		}
		return slug
	}
}
