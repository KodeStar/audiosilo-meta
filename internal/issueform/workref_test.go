package issueform

import "testing"

// TestResolveWorkRefAcceptsThePagePath covers the shape metaserve now serves a
// work at, /works/{slug}. It is what a submitter copies out of the address bar,
// so the correction and sidecar forms have to understand it - and it has to stay
// disjoint from the two shapes that were already accepted, which is what the
// rejection half pins.
func TestResolveWorkRefAcceptsThePagePath(t *testing.T) {
	const slug = "project-hail-mary"
	cases := []struct {
		name string
		ref  string
		want string
	}{
		{"absolute page URL", "https://meta.audiosilo.app/works/" + slug, slug},
		{"with a query string", "https://meta.audiosilo.app/works/" + slug + "?utm_source=x", slug},
		{"with a fragment", "https://meta.audiosilo.app/works/" + slug + "#recordings", slug},
		{"with a trailing slash", "https://meta.audiosilo.app/works/" + slug + "/", slug},
		{"a bare absolute path", "/works/" + slug, slug},
		{"a bare relative path", "works/" + slug, slug},
		{"a percent-encoded segment", "https://meta.audiosilo.app/works/" + "project%2Dhail%2Dmary", slug},
		// The shapes that already worked keep working, unchanged.
		{"the legacy query URL", "https://meta.audiosilo.app/work?id=" + slug, slug},
		{"the data-tree path", "data/works/pr/" + slug + "/work.json", slug},
		{"a bare slug", slug, slug},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveWorkRef(tc.ref)
			if !ok || got != tc.want {
				t.Errorf("resolveWorkRef(%q) = %q, %v; want %q", tc.ref, got, ok, tc.want)
			}
		})
	}
}

// TestWorkPageSlugIsStrict pins what the page rule declines. Each of these means
// something else, and a loose reading here would silently rewrite a submitter's
// reference into a work that is not the one they named.
func TestWorkPageSlugIsStrict(t *testing.T) {
	for _, path := range []string{
		"/works/a/b",             // two segments: that is the data-tree path's shape
		"/api/v1/works/x",        // an API endpoint, not a page
		"/people/andy-weir",      // another family
		"/works/",                // no slug at all
		"/works",                 // the family, not a record
		"/works/Not A Slug",      // not slug-shaped once decoded
		"/prefix/works/the-slug", // the page path is the WHOLE path or nothing
	} {
		if slug, ok := workPageSlug(path); ok {
			t.Errorf("workPageSlug(%q) = %q, want no match", path, slug)
		}
	}
}

// TestAPIWorkURLKeepsItsBehaviour is the regression half of the strictness: an
// /api/v1/works/{id} URL resolved to nothing before the page rule existed, and
// must still, so the page rule cannot quietly widen what a correction form
// accepts.
func TestAPIWorkURLKeepsItsBehaviour(t *testing.T) {
	if slug, ok := resolveWorkRef("https://meta.audiosilo.app/api/v1/works/project-hail-mary"); ok {
		t.Errorf("resolveWorkRef(api URL) = %q, want no resolution", slug)
	}
}

// TestPageURLBeatsTheDataTreePath pins the ORDER for the one URL both rules could
// read: /works/a/b has two segments, so the page rule declines and the data-tree
// reading (the GitHub blob links older issues carry) still applies.
func TestPageURLBeatsTheDataTreePath(t *testing.T) {
	got, ok := resolveWorkRef("https://github.com/KodeStar/audiosilo-meta/blob/main/data/works/pr/project-hail-mary/work.json")
	if !ok || got != "project-hail-mary" {
		t.Errorf("resolveWorkRef(blob URL) = %q, %v", got, ok)
	}
}
