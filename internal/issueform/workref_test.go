package issueform

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

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
		// A pasted path keeps its query or fragment, and url.Parse is not there to
		// cut it off: unstripped, the whole "the-thing?tab=chapters" failed the
		// slug check and was slugified into a reference naming nothing.
		{"a bare path with a query string", "/works/" + slug + "?tab=chapters", slug},
		{"a bare path with a fragment", "works/" + slug + "#recordings", slug},
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

// TestResolveRecordRefAcceptsThePagePaths covers the reference the correct-data
// form calls the easiest one: the page URL. All three families have a page, so
// all three have to resolve - a person or series page URL used to fall through
// every rule and be slugified into a work reference that named nothing.
func TestResolveRecordRefAcceptsThePagePaths(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		wantKind model.Kind
		wantSlug string
	}{
		{"work page URL", "https://meta.audiosilo.app/works/project-hail-mary", model.KindWork, "project-hail-mary"},
		{"person page URL", "https://meta.audiosilo.app/people/andy-weir", model.KindPerson, "andy-weir"},
		{"series page URL", "https://meta.audiosilo.app/series/the-stormlight-archive", model.KindSeries, "the-stormlight-archive"},
		{"with a query string", "https://meta.audiosilo.app/people/andy-weir?utm_source=x", model.KindPerson, "andy-weir"},
		{"with a fragment", "https://meta.audiosilo.app/series/the-stormlight-archive#books", model.KindSeries, "the-stormlight-archive"},
		{"with a trailing slash", "https://meta.audiosilo.app/people/andy-weir/", model.KindPerson, "andy-weir"},
		{"a bare absolute path", "/people/andy-weir", model.KindPerson, "andy-weir"},
		{"a bare relative path", "series/the-stormlight-archive", model.KindSeries, "the-stormlight-archive"},
		{"a percent-encoded segment", "https://meta.audiosilo.app/people/andy%2Dweir", model.KindPerson, "andy-weir"},
		// The forms that already worked are untouched.
		{"the legacy query URL", "https://meta.audiosilo.app/person?id=andy-weir", model.KindPerson, "andy-weir"},
		{"the data-tree work path", "data/works/pr/project-hail-mary/work.json", model.KindWork, "project-hail-mary"},
		{"the data-tree person path", "people/an/andy-weir.json", model.KindPerson, "andy-weir"},
		{"the data-tree series path", "series/th/the-stormlight-archive.json", model.KindSeries, "the-stormlight-archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveRecordRef(tc.ref)
			if !ok || got.kind != tc.wantKind || got.slug != tc.wantSlug {
				t.Errorf("resolveRecordRef(%q) = %v/%q, %v; want %v/%q", tc.ref, got.kind, got.slug, ok, tc.wantKind, tc.wantSlug)
			}
		})
	}
}

// TestResolveRecordRefKeepsTheRecordingForms: a recording has no page URL - it
// is addressed inside its work - so the shapes that DO name one must keep
// working, and the page rule must not claim a path that names something else.
func TestResolveRecordRefKeepsTheRecordingForms(t *testing.T) {
	got, ok := resolveRecordRef("data/works/ha/harry-potter/recordings/stephen-fry.json")
	if !ok || got.kind != model.KindRecording || got.slug != "stephen-fry" || got.workSlug != "harry-potter" {
		t.Errorf("resolveRecordRef(recording path) = %v/%q/%q, %v", got.kind, got.slug, got.workSlug, ok)
	}
	// An API URL is not a page, and resolved to nothing before the page rule
	// existed - so it still must.
	if got, ok := resolveRecordRef("https://meta.audiosilo.app/api/v1/works/project-hail-mary"); ok {
		t.Errorf("resolveRecordRef(api URL) = %v/%q, want no resolution", got.kind, got.slug)
	}
}

// TestEntityPageRefIsStrict pins what the shared page rule declines. Each of
// these means something else, and a loose reading would rewrite a submitter's
// reference into a record they did not name.
func TestEntityPageRefIsStrict(t *testing.T) {
	for _, p := range []string{
		"/works/a/b",             // two segments: the data-tree path's shape
		"/api/v1/works/x",        // an API endpoint, not a page
		"/people/",               // the family, not a record
		"/people",                // ditto
		"/albums/x",              // not a family this project has
		"/works/Not A Slug",      // not slug-shaped once decoded
		"/prefix/works/the-slug", // the page path is the WHOLE path or nothing
	} {
		if got, ok := entityPageRef(p); ok {
			t.Errorf("entityPageRef(%q) = %v/%q, want no match", p, got.kind, got.slug)
		}
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
