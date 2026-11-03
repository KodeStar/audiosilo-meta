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
// A TRANCHE (an --ids selection) decodes only the pages that can hold its ids
// when the manifest records each page's id range (librivoxManifest.PageIDs,
// which the fetcher writes with the page): a page whose lowest-to-highest
// range misses the selection is never opened. Every page decoded is checked
// against its recorded range, and an id outside it (a page rewritten since)
// reads the snapshot whole, as does a snapshot whose manifest leaves any page
// out (one fetched before the ranges were recorded, a hand-assembled one).
const (
	librivoxPagesDir     = "pages"
	librivoxManifestFile = "manifest.json"
)

// librivoxPageName is the file a fetched page is written to, by its offset.
func librivoxPageName(offset int) string { return fmt.Sprintf("offset-%07d.json", offset) }

// librivoxPageIDs is the range of project ids one page holds, lowest and
// highest, as the fetcher recorded it.
type librivoxPageIDs struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

// pageIDRange is the lowest and highest readable id among books; ok=false when
// none has one.
func pageIDRange(books []LibrivoxProject) (librivoxPageIDs, bool) {
	var r librivoxPageIDs
	for _, b := range books {
		id := int(b.ID)
		if id <= 0 {
			continue
		}
		if r.First == 0 || id < r.First {
			r.First = id
		}
		r.Last = max(r.Last, id)
	}
	return r, r.First > 0
}

// librivoxPageSpan is one page file and the ids it can hold, [lo, hi]; 0 on
// both is unbounded (a page with no recorded range, never checked).
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
// can hold them where the manifest records their ranges (see above). warnings
// names each project of the pages read that was skipped for an unreadable id.
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
	whole := make([]librivoxPageSpan, len(names))
	for i, n := range names {
		whole[i] = librivoxPageSpan{name: n}
	}
	if ids == nil {
		projects, warnings, _, err := decodeLibrivoxPages(pagesDir, whole, nil)
		return projects, warnings, err
	}
	if ranges := recordedPageIDs(dir); ranges != nil {
		var selected []librivoxPageSpan
		indexed := true
		for _, n := range names {
			r, ok := ranges[n]
			if !ok {
				indexed = false
				break
			}
			if ids.overlaps(r.First, r.Last) {
				selected = append(selected, librivoxPageSpan{name: n, lo: r.First, hi: r.Last})
			}
		}
		if indexed {
			projects, warnings, inSpan, err := decodeLibrivoxPages(pagesDir, selected, ids.Has)
			if err != nil || inSpan {
				return projects, warnings, err
			}
		}
	}
	projects, warnings, _, err = decodeLibrivoxPages(pagesDir, whole, ids.Has)
	return projects, warnings, err
}

// recordedPageIDs is the manifest's per-page id ranges, nil when it has none
// (no manifest, or one written before the fetcher recorded them).
func recordedPageIDs(dir string) map[string]librivoxPageIDs {
	raw, err := os.ReadFile(filepath.Join(dir, librivoxManifestFile))
	if err != nil {
		return nil
	}
	var m librivoxManifest
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m.PageIDs
}

// decodeLibrivoxPages decodes the pages in order, keeping the projects keep
// selects (all when keep is nil), and warns about each project with no
// readable id. inSpan=false means a page held an id outside its span: the
// manifest no longer describes the page, and the caller reads it whole instead.
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
			if pg.hi > 0 && (id < pg.lo || id > pg.hi) {
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
