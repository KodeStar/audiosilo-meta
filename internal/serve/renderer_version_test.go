package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

// TestSitemapGolden renders the sitemap index and one shard over the shared
// fixture and diffs them against the committed output, as TestEntityPagesGolden
// does for the pages: a change to the XML shows up as a readable diff, and the
// renderer version guard below notices it. Regenerate with -update-golden.
func TestSitemapGolden(t *testing.T) {
	ts := sitemapSite(t, artifacttest.Fixture())
	for _, tc := range []struct{ file, path string }{
		{"sitemap-index.xml", sitemapIndexPath},
		{"sitemap-works-0.xml", sitemapShardPrefix + "works-0.xml"},
	} {
		code, body, _ := getSitemap(t, ts.URL, tc.path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.path, code)
		}
		assertGolden(t, tc.file, []byte(body))
	}
}

// rendererPins ties each renderer version constant to a digest of the goldens
// that renderer produces. The versions exist for builds without a VCS revision
// (Docker builds omit .git), where nothing else invalidates a cached page or
// sitemap when the representation changes - so a golden change that forgets
// the bump would serve stale validators silently. Changing a golden without
// changing its version fails here.
//
// When this test fails after a deliberate rendering change: bump the version
// constant (htmlRendererVersion in shell.go, sitemapRendererVersion in
// sitemap.go) AND update its pin below, both the version and the digest the
// failure prints. A change to the test FIXTURE alone (no new representation)
// updates the digest and leaves the version as it is.
var rendererPins = []struct {
	constant, version, glob, pinnedVersion, pinnedDigest string
}{
	{"htmlRendererVersion", htmlRendererVersion, "*.html", "3", "8a132446676d600e"},
	{"sitemapRendererVersion", sitemapRendererVersion, "sitemap-*.xml", "2", "58e9497474f1fd4d"},
}

func TestRendererVersionsTrackTheGoldens(t *testing.T) {
	for _, pin := range rendererPins {
		digest := goldenDigest(t, pin.glob)
		switch {
		case pin.version == pin.pinnedVersion && digest != pin.pinnedDigest:
			t.Errorf("the %s goldens changed but %s is still %q: if the rendered representation changed, bump %s and set its pin "+
				"in rendererPins to the new version and digest %s; if only the test fixture or a new golden case changed, "+
				"set the pin's digest to %s and keep the version",
				pin.glob, pin.constant, pin.version, pin.constant, digest, digest)
		case pin.version != pin.pinnedVersion:
			t.Errorf("%s is %q but its pin says %q: set the pin to version %q and digest %s",
				pin.constant, pin.version, pin.pinnedVersion, pin.version, digest)
		}
	}
}

// goldenDigest hashes every golden matching glob, name and bytes, in name order
// (filepath.Glob sorts), so adding, removing or renaming a golden moves it too.
func goldenDigest(t *testing.T, glob string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "golden", glob))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no goldens match %s", glob)
	}
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(filepath.Base(f) + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
