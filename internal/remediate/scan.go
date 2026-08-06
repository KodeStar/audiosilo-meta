package remediate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// scan.go reads the tree ONCE into the index the planner works from.
//
// It deliberately does not go through the Store's parse cache. The cache is a
// WRITER's cache (see pkg/pack.Reader): everything it holds stays resident, and
// the works family is ~480 MB of JSON, so reading the whole catalogue through it
// would keep the whole catalogue alive for the rest of the run to save re-reading
// the handful of packs the flush writes into. Each pack is parsed, harvested and
// released here; the Store re-reads what it writes to, which is what it always
// does.

// candidate is a work the remediation might touch: its title carries a part
// marker or a dramatized edition marker. Its entry is kept whole, because a
// merge composes from the record's real bytes, and the two questions every
// caller asks of its recordings - is this GraphicAudio, and is there exactly one
// production - are answered once at scan time. Decoding the recordings map on
// demand cost four to six full decodes of every candidate on every planning
// round.
type candidate struct {
	obj
	// graphicAudio reports that some recording names GraphicAudio as its
	// publisher, which is the cohort gate.
	graphicAudio bool
	// recKey and rec are the work's ONE recording, empty when it carries none
	// or several.
	recKey string
	rec    obj
}

// title returns the work's title.
func (c candidate) title() string { return c.str("title") }

// authors returns the work's author ids, in the order the record lists them.
func (c candidate) authors() []string { return c.strs("authors") }

// soleRecording returns the work's one recording. ok is false for a work with
// none or with several - which the merge refuses rather than guessing which
// production a part belongs to (the live tree has 14 such works, all of them
// carrying a duplicate recording credited only to the collective "full-cast"
// record; collapsing those is a different, unrequested change).
func (c candidate) soleRecording() (key string, rec obj, ok bool) {
	return c.recKey, c.rec, c.rec != nil
}

// index is the catalogue as the planner needs to see it.
type index struct {
	// candidates holds every work whose title carries a part or dramatized
	// marker, keyed by slug.
	candidates map[string]candidate
	// plain maps a (article-tolerant title key, author set) to the slugs of the
	// works that are NOT dramatizations - the plain text editions a hijacked
	// series slot belongs to.
	plain map[string][]string
	// works holds every work slug, so a minted slug can be checked for a
	// collision before it is claimed.
	works map[string]bool
	// series holds every series entry, keyed by series slug. The family is
	// small (30,799 entries in 57 packs) and every one of them has to be
	// checked for a reference to a part work, so it is read whole.
	series map[string]obj
	// seriesOf maps a work slug to the series that name it, so a repair pass
	// visits the series a rewrite reaches instead of all 30,799.
	seriesOf map[string][]string
}

// plainKey is the key the plain index is built and probed with.
func plainKey(title string, authors []string) string {
	return titleKey(title) + "\x01" + authorsKey(authors)
}

// lightWork is the slice of a work entry the plain index needs from the
// 133,706 works the remediation will never touch.
type lightWork struct {
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
}

// scan reads dataDir's works and series families into an index.
func scan(dataDir string, store *pack.Store) (*index, error) {
	idx := &index{
		candidates: map[string]candidate{},
		plain:      map[string][]string{},
		works:      map[string]bool{},
		series:     map[string]obj{},
		seriesOf:   map[string][]string{},
	}
	for _, ref := range store.Tree(pack.FamilyWorks).Packs() {
		file, err := readPack(dataDir, ref)
		if err != nil {
			return nil, err
		}
		for _, slug := range file.Slugs() {
			raw, _ := file.Get(slug)
			if err := idx.addWork(slug, raw); err != nil {
				return nil, fmt.Errorf("%s: entry %s: %w", ref.Path(), slug, err)
			}
		}
	}
	for _, ref := range store.Tree(pack.FamilySeries).Packs() {
		file, err := readPack(dataDir, ref)
		if err != nil {
			return nil, err
		}
		for _, slug := range file.Slugs() {
			raw, _ := file.Get(slug)
			o, err := decodeObj(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: entry %s: %w", ref.Path(), slug, err)
			}
			idx.series[slug] = o
			works, err := seriesWorks(o)
			if err != nil {
				return nil, fmt.Errorf("%s: entry %s: %w", ref.Path(), slug, err)
			}
			for _, sw := range works {
				idx.seriesOf[sw.Work] = append(idx.seriesOf[sw.Work], slug)
			}
		}
	}
	return idx, nil
}

// addWork files one work entry into the index.
func (idx *index) addWork(slug string, raw json.RawMessage) error {
	idx.works[slug] = true
	var light lightWork
	if err := json.Unmarshal(raw, &light); err != nil {
		return err
	}
	_, isPart := partOf(light.Title)
	if !isPart && !isDramatized(light.Title) {
		// A plain edition: a candidate for a hijacked series slot. Nothing
		// else about it is needed, so nothing else is kept.
		key := plainKey(light.Title, light.Authors)
		idx.plain[key] = append(idx.plain[key], slug)
		return nil
	}
	o, err := decodeObj(raw)
	if err != nil {
		return err
	}
	c := candidate{obj: o}
	recs, err := o.recordings()
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(recs) {
		if graphicAudioPublishers[recs[key].str("publisher")] {
			c.graphicAudio = true
		}
	}
	if len(recs) == 1 {
		for key, rec := range recs {
			c.recKey, c.rec = key, rec
		}
	}
	idx.candidates[slug] = c
	return nil
}

// plainTwins returns the plain text works recorded for a base title and author
// set. The caller decides what to do with a count other than one: two
// candidates are an ambiguity a human resolves, never a coin toss (the live
// tree has three - "Cherish", "Stone of Farewell", "The Kingdom of Liars").
func (idx *index) plainTwins(base string, authors []string) []string {
	return idx.plain[plainKey(base, authors)]
}

// readPack parses one pack file off disk, outside the store's cache.
//
// It is a third copy of the read-and-parse convention (pkg/pack's Reader and
// check's readPack are the others) and stays local on purpose: the reason it
// exists is precisely that it must NOT go through a cache, so a shared helper
// would have to carry that as an option.
func readPack(dataDir string, ref pack.PackRef) (*pack.File, error) {
	abs := filepath.Join(dataDir, filepath.FromSlash(ref.Path()))
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	file, err := pack.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref.Path(), err)
	}
	return file, nil
}
