package serve

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

// The three markers the BUILT Astro shell carries, and the whole contract
// between this package and the site build:
//
//   - in <head>, headOpenMarker ... headCloseMarker wrap the replaceable block
//     (title, description, canonical, og/twitter). Everything outside them -
//     charset, viewport, favicons, theme-color, the generator tag - is the
//     shell's and is never touched.
//   - in <body>, bodyMarker sits immediately before the React island's mount
//     point and is REPLACED by the server-rendered fact sheet plus the embedded
//     entity payload.
//
// They are HTML comments on purpose: a shell that is served untouched (no
// snapshot, an older dist, the dev server) is still a valid page, and the
// markers say out loud where the injection happens for anyone reading the built
// output.
const (
	headOpenMarker  = "<!--ssr:head-->"
	headCloseMarker = "<!--/ssr:head-->"
	bodyMarker      = "<!--ssr:entity-->"
)

// shell is one built page, held both as it is on disk and split around its
// markers, so a request is a concatenation of literals plus the two composed
// parts rather than a search over hundreds of kilobytes of HTML. The head
// markers are KEPT (they are comments, and they make the injection visible in
// the served page); the body marker is consumed by what replaces it.
//
// inject is false for a page that carries no usable markers - an older dist, or
// a Base.astro refactor that dropped one. The page is then still SERVED, exactly
// as the file reads, which is the whole point of the degrade: the feature turns
// off, the site does not.
type shell struct {
	raw    string // the built page exactly as it is on disk
	inject bool
	head   string // everything up to and including headOpenMarker
	mid    string // headCloseMarker up to (not including) bodyMarker
	tail   string // everything after bodyMarker
	// identity is the non-data half of this page's ETag, mixed at load - see
	// pageIdentity. Empty on a shell that carries no markers, which issues no
	// validator at all (serveRaw).
	identity string
}

// render assembles the page: the shell's own head, the composed head block, the
// rest of the shell up to the body slot, the composed entity block, the rest.
func (sh *shell) render(head, body string) string {
	var b strings.Builder
	b.Grow(len(sh.head) + len(head) + len(sh.mid) + len(body) + len(sh.tail))
	b.WriteString(sh.head)
	b.WriteString(head)
	b.WriteString(sh.mid)
	b.WriteString(body)
	b.WriteString(sh.tail)
	return b.String()
}

// serveRaw writes the built page untouched. It carries no validator: an
// un-injected shell is the same bytes for every id, so an ETag would tell a
// client its copy of one record's page is good for another's.
func (sh *shell) serveRaw(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, sh.raw)
}

// splitShell reads a built page as a shell. Each marker must appear EXACTLY
// once and in order; anything else is "this dist does not support injection",
// reported as an error so the caller can log it once and serve the file
// untouched. Requiring exactly one occurrence rather than taking the first is
// what keeps a half-refactored Base.astro from producing a page whose head is
// replaced in one place and left stale in another.
func splitShell(page string) (*shell, error) {
	for _, marker := range []string{headOpenMarker, headCloseMarker, bodyMarker} {
		if n := strings.Count(page, marker); n != 1 {
			return nil, fmt.Errorf("marker %s appears %d times, want exactly 1", marker, n)
		}
	}
	open := strings.Index(page, headOpenMarker) + len(headOpenMarker)
	closing := strings.Index(page, headCloseMarker)
	body := strings.Index(page, bodyMarker)
	if open > closing || closing > body {
		return nil, fmt.Errorf("markers are out of order (%s, %s, %s)", headOpenMarker, headCloseMarker, bodyMarker)
	}
	return &shell{
		raw:    page,
		inject: true,
		head:   page[:open],
		mid:    page[closing:body],
		tail:   page[body+len(bodyMarker):],
	}, nil
}

// shells holds every built entity shell, keyed by its path under the site
// directory. It is built ONCE, at construction: the dist a container serves is
// immutable for the process's life, so re-reading and re-splitting per request
// would buy nothing, and the reason a shell cannot be injected into is logged
// there rather than once per request.
//
// A shell that is MISSING or unreadable is absent from the map, and its route
// then falls through to the static site handler exactly as it did before these
// routes existed. A shell that is present but UNMARKED is in the map with
// inject=false: the page is served as built.
type shells map[string]*shell

// get returns the split shell for name, or nil when this dist carries no
// injectable shell for it.
func (s shells) get(name string) *shell {
	if s == nil {
		return nil
	}
	return s[name]
}

// loadShells splits every entity shell under dir and stamps each with its page
// identity (siteURL is the configured public origin - see pageIdentity). It
// never fails: the whole point of the marker contract is that its absence
// degrades to the static page.
func loadShells(dir, siteURL string, logger *log.Logger) shells {
	out := shells{}
	for _, e := range htmlEntityRoutes {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(e.shell)))
		if err != nil {
			// Named by the SHELL, which is unique per page: several pages hang off
			// one family prefix now (the guide pages), so a notice naming the prefix
			// could not say which of them the dist is missing.
			logger.Printf("serve: no server-rendered %s page: %v", e.shell, err)
			continue
		}
		sh, err := splitShell(string(raw))
		if err != nil {
			logger.Printf("serve: %s carries no injection markers, serving it untouched: %v", e.shell, err)
			out[e.shell] = &shell{raw: string(raw)}
			continue
		}
		sh.identity = pageIdentity(e.shell, string(raw), siteURL, buildRevision())
		out[e.shell] = sh
	}
	return out
}

// pageIdentity is the non-data half of an entity page's ETag: everything that
// changes the bytes we render while the ARTIFACT stays the same. The validator
// has to cover it, because a client whose 304 is renewed against an unchanged
// artifact stays on the page it cached - and a UI-only deploy (a new dist, the
// same data release) leaves that page asking for hashed assets the server no
// longer has, forever.
//
// Four components, mixed into one short hash at load because none of them
// changes for the life of the process:
//
//   - the PAGE's own name (its shell path): what distinguishes two pages that a
//     record can be addressed at. A work and its recap page are different
//     representations of one slug, and their shells are two files - but nothing
//     stops a build from emitting the same bytes for both (a shell is mostly
//     layout), and two representations sharing a validator would hand a client
//     the wrong 304. The name is the one component that cannot coincide;
//   - the built shell's own bytes: the dist's identity, and the only component a
//     UI-only deploy moves;
//   - the configured site URL: every canonical link, og:url and JSON-LD url is
//     built from it, so one artifact renders two different pages at two origins;
//   - the binary's VCS revision: the COMPOSER's own code, which a fact-sheet or
//     JSON-LD change moves without touching any of the above.
//
// fnv-64a is enough: this is a cache key, not a signature - a client that
// fabricates a validator is only ever handed a 304 for a page it invented (see
// entityHandler), so nothing here has to resist collisions chosen by an
// attacker. Components are length-prefixed so no two of them can be run together
// into the same digest input.
func pageIdentity(shellName, shellHTML, siteURL, revision string) string {
	h := fnv.New64a()
	for _, part := range []string{shellName, shellHTML, siteURL, revision} {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(part)))
		_, _ = h.Write(n[:])
		_, _ = io.WriteString(h, part)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// buildRevision is the commit this binary was built from, or "" when the
// toolchain stamped none - a `go run`, a test binary, a -buildvcs=false build.
// Empty is honest rather than an error: the validator then rests on the two
// components that are always there, and an unstamped binary is not what a
// crawler's cached copy faces (the released image is built from a checkout).
var buildRevision = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
})
