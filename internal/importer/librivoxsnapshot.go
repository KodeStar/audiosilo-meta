package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The snapshot layout (written by LibrivoxFetcher.Fetch, read here):
//
//	<snapshot>/manifest.json            the fetch's resume state (librivoxManifest)
//	<snapshot>/pages/<name>.json        one feed response body each, verbatim
//
// The reader needs only pages/: every *.json file under it is a {"books": [...]}
// body, read in name order. A project id seen twice keeps its LAST copy (a
// later page is a later reading), which never happens in a fetch that ran to
// completion but makes a hand-assembled snapshot unambiguous. A project whose
// id is missing or unreadable (lvInt's 0, or a negative number) can be selected
// by no --ids and keyed by nothing: it is skipped with a warning naming its page
// and title, never kept as project 0.
//
// A TRANCHE (an --ids selection) reads only the pages that can hold its ids
// when the snapshot is one the fetcher wrote: a manifest beside pages named
// offset-NNNNNNN.json. The fetcher pages the feed's default order, ascending
// project id, so page i holds the ids from its own first id up to the next
// page's first id, exclusive; each page's first id is read off the head of the
// file alone (librivoxFirstID), and only the pages whose span meets the
// selection are decoded. Every page decoded is checked against its span: an id
// outside it means the order did not hold, and the snapshot is then read whole,
// as is a snapshot that is not fetcher-shaped (a hand-assembled one, the test
// fixtures), or one whose first ids do not ascend.
const (
	librivoxPagesDir     = "pages"
	librivoxManifestFile = "manifest.json"
)

// librivoxPageSpan is one page file and the ids it can hold, [lo, hi]; lo 0 and
// hi 0 are unbounded.
type librivoxPageSpan struct {
	name   string
	lo, hi int
}

// ReadLibrivoxSnapshot reads every project a snapshot holds, sorted by id. A
// project with no readable id is left out (RunLibrivox warns about it).
func ReadLibrivoxSnapshot(dir string) ([]LibrivoxProject, error) {
	projects, _, err := readLibrivoxSnapshot(dir, nil)
	return projects, err
}

// readLibrivoxSnapshot is ReadLibrivoxSnapshot keeping only the projects ids
// selects (every project when ids is nil), so a tranche import holds only its
// own projects rather than the whole catalogue, and decodes only the pages that
// can hold them where the snapshot's order allows (see above). warnings names
// each project of the pages read that was skipped for an unreadable id.
func readLibrivoxSnapshot(dir string, ids *LibrivoxIDs) (projects []LibrivoxProject, warnings []string, err error) {
	pagesDir := filepath.Join(dir, librivoxPagesDir)
	entries, err := os.ReadDir(pagesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read LibriVox snapshot %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if ids == nil {
		projects, warnings, _, err := decodeLibrivoxPages(pagesDir, unboundedSpans(names), nil)
		return projects, warnings, err
	}
	if spans, ok := librivoxPageIndex(dir, names); ok {
		var selected []librivoxPageSpan
		for _, sp := range spans {
			if ids.overlaps(sp.lo, sp.hi) {
				selected = append(selected, sp)
			}
		}
		projects, warnings, inSpan, err := decodeLibrivoxPages(pagesDir, selected, ids.Has)
		if err != nil || inSpan {
			return projects, warnings, err
		}
	}
	projects, warnings, _, err = decodeLibrivoxPages(pagesDir, unboundedSpans(names), ids.Has)
	return projects, warnings, err
}

// unboundedSpans is every page with no span: decoded, never checked.
func unboundedSpans(names []string) []librivoxPageSpan {
	spans := make([]librivoxPageSpan, len(names))
	for i, n := range names {
		spans[i] = librivoxPageSpan{name: n}
	}
	return spans
}

// decodeLibrivoxPages decodes the pages in order, keeping the projects keep
// selects (all when keep is nil), and warns about each project with no
// readable id. inSpan=false means a page held an id outside its span: the
// snapshot's order did not hold, and the caller reads it whole instead.
func decodeLibrivoxPages(pagesDir string, pages []librivoxPageSpan, keep func(id int) bool) (projects []LibrivoxProject, warnings []string, inSpan bool, err error) {
	byID := map[int]LibrivoxProject{}
	for _, pg := range pages {
		path := filepath.Join(pagesDir, pg.name)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, false, err
		}
		books, err := parseLibrivoxPage(body)
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s: %w", path, err)
		}
		for _, b := range books {
			id := int(b.ID)
			if id <= 0 {
				warnings = append(warnings, fmt.Sprintf("librivox snapshot %s: project %q has no readable id; skipped (no --ids can select it)", path, b.Title))
				continue
			}
			if (pg.lo > 0 && id < pg.lo) || (pg.hi > 0 && id > pg.hi) {
				return nil, nil, false, nil
			}
			if keep == nil || keep(id) {
				byID[id] = b
			}
		}
	}
	out := make([]LibrivoxProject, 0, len(byID))
	for _, b := range byID {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, warnings, true, nil
}

// librivoxPageIndex is the spans of a fetcher-written snapshot's pages (see the
// layout comment), ok=false when dir is not one: no manifest, a page not named
// offset-NNNNNNN.json, a page whose first id cannot be read, or first ids that
// do not ascend in page order.
func librivoxPageIndex(dir string, names []string) ([]librivoxPageSpan, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, librivoxManifestFile))
	if err != nil {
		return nil, false
	}
	var m librivoxManifest
	if json.Unmarshal(raw, &m) != nil || m.Feed == "" {
		return nil, false
	}
	spans := make([]librivoxPageSpan, 0, len(names))
	for _, n := range names {
		var offset int
		if len(n) != len("offset-0000000.json") || !strings.HasPrefix(n, "offset-") {
			return nil, false
		}
		if _, err := fmt.Sscanf(n, "offset-%07d.json", &offset); err != nil {
			return nil, false
		}
		first, ok := librivoxFirstID(filepath.Join(dir, librivoxPagesDir, n))
		if !ok {
			return nil, false
		}
		if k := len(spans); k > 0 {
			if first <= spans[k-1].lo {
				return nil, false
			}
			spans[k-1].hi = first - 1
		}
		spans = append(spans, librivoxPageSpan{name: n, lo: first})
	}
	return spans, true
}

// librivoxFirstID reads the id of a page's first project off the head of the
// file, decoding nothing past it: the feed writes "id" as each project's first
// key, so this reads a few hundred bytes of a page. ok=false for a page it
// cannot read that way (no books, no readable id).
func librivoxFirstID(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	if !nextDelim(dec, '{') {
		return 0, false
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return 0, false
		}
		if key != "books" {
			if !skipValue(dec) {
				return 0, false
			}
			continue
		}
		if !nextDelim(dec, '[') || !dec.More() || !nextDelim(dec, '{') {
			return 0, false
		}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return 0, false
			}
			if k == "id" {
				var id lvInt
				if dec.Decode(&id) != nil || id <= 0 {
					return 0, false
				}
				return int(id), true
			}
			if !skipValue(dec) {
				return 0, false
			}
		}
		return 0, false
	}
	return 0, false
}

// nextDelim reads the next token and reports whether it is the delimiter d.
func nextDelim(dec *json.Decoder, d json.Delim) bool {
	tok, err := dec.Token()
	return err == nil && tok == d
}

// skipValue reads past the next value.
func skipValue(dec *json.Decoder) bool {
	var skip json.RawMessage
	return dec.Decode(&skip) == nil
}
