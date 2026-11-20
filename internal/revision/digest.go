// Package revision dates metaserve's pages by their last MEANINGFUL content
// change, across data releases, without walking git history.
//
// A page's revision is decided by comparing two artifacts, not by asking what
// changed in the data: Digests reduces every page of an artifact to a 128-bit
// digest of exactly what the page renders from (fields.go decides what that is,
// field by field), and Stamp digests the NEW artifact and the PREVIOUS release's
// with the same code, then dates each page:
//
//   - same digest: the previous release's date for it, carried unchanged - or
//     no date at all, when the previous release knew none (an artifact older
//     than the revision layer, or a page unchanged since the chain started);
//   - different digest, or a page the previous release did not have: the
//     stamp this release is cut at (Options.At).
//
// Comparing the RENDERED inputs rather than the records is what makes a change
// reach every page it shows on and no other: a renamed narrator moves their own
// page, every work crediting them, every page listing those works by narrator
// and nothing else, because only those pages read the name. And because both
// artifacts are digested by the SAME code, a change to this package, to pkg/query
// or to the schema moves no date by itself - a new field read from both sides is
// equal on both sides wherever the data did not change.
//
// The result is written into the artifact's page_revisions table
// (internal/build creates it empty, schema_version 14) and read back by
// pkg/query's Pages and PageRevision for the sitemap's <lastmod>, the pages'
// JSON-LD dateModified and their Last-Modified header.
package revision

import (
	"context"
	"crypto/sha256"
	"reflect"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// Key names one page: its family (spelled as the sitemaps and page_revisions
// spell it) and the record id it is addressed by.
type Key struct {
	Family query.PageFamily
	ID     string
}

// Digest is a page's content digest: the first 128 bits of a SHA-256, which is
// far past any chance of two different pages colliding in a catalogue of a
// million pages, at half the memory of the whole sum.
type Digest [16]byte

// enumWindow is how many ids one Pages read lists while the pages are enumerated.
const enumWindow = 50000

// Digests reduces every page of db to its content digest, on workers goroutines
// (at least one). The families are the sitemap's: works, people and series, and
// the two guide families, which are digested from the same work detail as the
// work page they hang off and exist exactly where metaserve serves them
// (query.HasRecapGuide, query.HasCharacterGuide). It keeps one idle connection
// per worker on db (query.DB.SetMaxIdleConns) for the reads it makes.
func Digests(ctx context.Context, db *query.DB, workers int) (map[Key]Digest, error) {
	workers = max(workers, 1)
	// One open connection per worker, kept: database/sql's default keeps two, and
	// every other worker's next query would reopen one and re-parse the schema.
	db.SetMaxIdleConns(workers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make(map[Key]Digest, db.PageCount(query.WorkPages)+db.PageCount(query.PersonPages)+db.PageCount(query.SeriesPages))
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}
	jobs := make(chan Key, 4*workers)
	for range workers {
		wg.Go(func() {
			d := pageDigester{ctx: ctx, db: db}
			for k := range jobs {
				if ctx.Err() != nil {
					continue
				}
				pages, err := d.digest(k)
				if err != nil {
					fail(err)
					continue
				}
				mu.Lock()
				for _, p := range pages {
					out[p.key] = p.digest
				}
				mu.Unlock()
			}
		})
	}

feed:
	for _, family := range []query.PageFamily{query.WorkPages, query.PersonPages, query.SeriesPages} {
		for offset := 0; ; offset += enumWindow {
			entries, err := db.Pages(family, enumWindow, offset)
			if err != nil {
				fail(err)
				break feed
			}
			for _, e := range entries {
				select {
				case jobs <- Key{Family: family, ID: e.ID}:
				case <-ctx.Done():
					break feed
				}
			}
			if len(entries) < enumWindow {
				break
			}
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type pageDigest struct {
	key    Key
	digest Digest
}

// pageDigester digests the page(s) one enumerated record stands for.
type pageDigester struct {
	ctx context.Context
	db  *query.DB
}

func (p pageDigester) digest(k Key) ([]pageDigest, error) {
	switch k.Family {
	case query.WorkPages:
		return p.work(k.ID)
	case query.PersonPages:
		return p.person(k.ID)
	default:
		return p.series(k.ID)
	}
}

// digestOf hashes the documents one page renders from, under the page's family
// name so two families' pages cannot share a digest.
func digestOf(family query.PageFamily, docs ...any) (Digest, error) {
	e := encoder{h: sha256.New()}
	e.str(string(family))
	for _, doc := range docs {
		if err := e.value(reflect.ValueOf(doc)); err != nil {
			return Digest{}, err
		}
	}
	var d Digest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}

// work digests a work page and the guide pages hanging off it, from ONE read of
// its detail. The work page also stands for its recordings' chapters pages
// (/works/{id}/recordings/{recording}/chapters, which carry the work's date), so
// every recording's whole chapter list is read here. Those pages render the
// chapters beside the work card and the recording summary, and both of those are
// composed from facts the work detail already carries - so the chapter list is
// read on its own (query.DB.Chapters), not through RecordingChapters' card and
// summary reads, which were a third of the whole digest's time on the real
// catalogue. A recording that states no chapters has nothing to add.
func (p pageDigester) work(id string) ([]pageDigest, error) {
	d, err := p.db.WorkDetail(id)
	if err != nil || d == nil {
		return nil, err
	}
	docs := []any{workFacts{
		Detail:          d,
		RecapGuide:      query.HasRecapGuide(d),
		CharacterGuide:  query.HasCharacterGuide(d),
		ChapteredRecaps: query.HasChapteredRecaps(d),
		PositionUnit:    positionUnit(d),
	}}
	for _, rec := range d.Recordings {
		if rec.ChapterCount == 0 {
			continue
		}
		chapters, err := p.db.Chapters(id, rec.ID)
		if err != nil {
			return nil, err
		}
		docs = append(docs, recordingChapters{Recording: rec.ID, Chapters: chapters})
	}
	workDigest, err := digestOf(query.WorkPages, docs...)
	if err != nil {
		return nil, err
	}
	out := []pageDigest{{Key{query.WorkPages, id}, workDigest}}

	common := guideFacts{Title: d.Title, Authors: d.Authors, Cover: firstCover(d), PositionUnit: positionUnit(d)}
	if len(d.Series) > 0 {
		common.SeriesID, common.SeriesName = d.Series[0].ID, d.Series[0].Name
	}
	if query.HasRecapGuide(d) {
		g := common
		g.Sibling = query.HasCharacterGuide(d)
		g.Recaps, g.RecapSummary = d.Recaps, d.RecapSummary
		dg, err := digestOf(query.RecapPages, g)
		if err != nil {
			return nil, err
		}
		out = append(out, pageDigest{Key{query.RecapPages, id}, dg})
	}
	if query.HasCharacterGuide(d) {
		g := common
		g.Sibling = query.HasRecapGuide(d)
		g.Characters = d.Characters
		dg, err := digestOf(query.CharacterPages, g)
		if err != nil {
			return nil, err
		}
		out = append(out, pageDigest{Key{query.CharacterPages, id}, dg})
	}
	return out, nil
}

// person digests a person page: the credit window the page shows (the same
// window metaserve composes it with, query.PersonPageMax) and every page of the
// person's recording credits.
func (p pageDigester) person(id string) ([]pageDigest, error) {
	d, err := p.db.Person(id, query.PersonPageMax, 0)
	if err != nil || d == nil {
		return nil, err
	}
	docs := []any{d}
	for offset := 0; ; offset += query.CataloguePageMax {
		credits, err := p.db.PersonRecordings(p.ctx, id, query.CataloguePageMax, offset, "", "")
		if err != nil {
			return nil, err
		}
		if credits == nil {
			break
		}
		docs = append(docs, credits)
		if len(credits.Recordings) == 0 || offset+len(credits.Recordings) >= credits.Total {
			break
		}
	}
	dg, err := digestOf(query.PersonPages, docs...)
	if err != nil {
		return nil, err
	}
	return []pageDigest{{Key{query.PersonPages, id}, dg}}, nil
}

// series digests a series page: every page of its listening guide, which is
// what the page and its JSON-LD are composed from.
func (p pageDigester) series(id string) ([]pageDigest, error) {
	var docs []any
	for offset := 0; ; offset += query.CataloguePageMax {
		guide, err := p.db.SeriesListeningGuide(p.ctx, id, query.CataloguePageMax, offset)
		if err != nil {
			return nil, err
		}
		if guide == nil {
			break
		}
		docs = append(docs, guide)
		if len(guide.Entries) == 0 || offset+len(guide.Entries) >= guide.WorksTotal {
			break
		}
	}
	if len(docs) == 0 {
		return nil, nil
	}
	dg, err := digestOf(query.SeriesPages, docs...)
	if err != nil {
		return nil, err
	}
	return []pageDigest{{Key{query.SeriesPages, id}, dg}}, nil
}

// positionUnit is the unit a position counts on this work's pages, as metaserve
// words it: the unit the work's position_scale states, else "" for the default
// (chapters). The guide titles, row labels and the work page's recap link all
// read it; the scale's length and partial flag reach no page.
func positionUnit(d *query.WorkDetail) string {
	if d.PositionScale == nil {
		return ""
	}
	return d.PositionScale.Unit
}

// firstCover is the cover a guide page shows (its og:image): the first recording
// that states one.
func firstCover(d *query.WorkDetail) string {
	for _, rec := range d.Recordings {
		if rec.CoverURL != "" {
			return rec.CoverURL
		}
	}
	return ""
}
