package importer

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// store.go is the STORE-LISTING import (`metaimport store`): a maintainer's
// reviewed file of a store's public product listings (Audiobook Guild,
// GraphicAudio: pkg/model's storelinks.go), each a book the identifier-derived
// retailers do not reach, landed through the ordinary create path - the work
// identity walk, the duplicate-identity guard, the person rules, series
// resolution and (with AttachEditions) the attach rule - under source type
// `store-listing` (LICENSING.md "Store listings").
//
// The input is the libex row shape (NDJSON, or one JSON array), so a maintainer
// writes it with the keys the libex import already reads:
//
//	{"store":"audiobookguild","url":"https://audiobookguild.com/products/without-law-17",
//	 "title":"Without Law 17","subtitle":"...","authors":[{"name":"Eric Vall"}],
//	 "narrators":[{"name":"Joshua Story"}],"series":[{"name":"Without Law","position":"17"}],
//	 "lengthMinutes":612,"releaseDate":"2024-03-05","publisher":"Royal Guard Publishing",
//	 "imageUrl":"https://cdn.shopify.com/...","language":"english"}
//
// `store` and `url` are the row's identity (the store's page, held as stated on
// the recording's links and deduplicated through model.StoreLinkKey); every
// other key is a fact the row may omit. NOTHING ELSE is read: a description, a
// rating or a genre in the file is never looked at (facts only, and a store's
// genre strings are its taxonomy). A row carrying an `asin` is refused - it is a
// retail listing with an identifier, and belongs to the libex import, whose
// dedup and attestation rules key on that identifier.
//
// A row whose book the catalogue already holds as the SAME production (the
// work the identity walk reaches, a recording with the row's narrators, a
// compatible runtime and no abridgement or series-position conflict) adds its
// store page to that recording when it states none for that store, rather than
// minting a twin; one that states a different page for it is refused with a
// warning (two pages for one production is a maintainer's question).

// storeFacts is a store-listing row's identity: which store and its page.
type storeFacts struct {
	key string // a model.StoreKeys value
	url string
}

// storeParse is the outcome of decoding a store-listing file: the usable rows,
// the count of rows refused at parse, and one warning per refusal or dropped
// fact.
type storeParse struct {
	books    []sourceBook
	skipped  int
	warnings []string
}

// RunStore imports path (a store-listing file) into opts.DataDir. Only the
// create mode exists for it: the catalogue-bounded passes key on an ASIN, which
// a store row never carries.
func RunStore(path string, opts Options) (Summary, error) {
	if opts.Mode != ModeCreate {
		return Summary{}, fmt.Errorf("a store-listing import only creates (with --attach-editions and --existing-series-only); the other modes key on an ASIN, which a store row has none of")
	}
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	parsed, err := parseStoreRows(f)
	if err != nil {
		return Summary{}, err
	}
	sum, runErr := runBooks(parsed.books, model.SourceStoreListing, opts, nil)
	sum.SkippedRows += parsed.skipped
	sum.Warnings = append(sum.Warnings, parsed.warnings...)
	return sum, runErr
}

// parseStoreRows decodes a store-listing file row by row through the libex
// reader (NDJSON or one array), so the two files share one decoder; a lone
// object is a row when it names a store.
func parseStoreRows(r io.Reader) (storeParse, error) {
	var sp storeParse
	if err := streamRows(r, false, "store", func(_ []byte, e rawBook) { sp.parseRow(e) }); err != nil {
		return storeParse{}, fmt.Errorf("store listings: %w", err)
	}
	return sp, nil
}

// refuse records a row the parse layer will not hand on.
func (sp *storeParse) refuse(label, format string, args ...any) {
	sp.skipped++
	sp.warnings = append(sp.warnings, "store: "+label+": "+fmt.Sprintf(format, args...)+"; row skipped")
}

// parseRow parses one decoded row into sp. The parse layer refuses what only it
// can judge - the identity (a known store and an https page on that store's
// host), an ASIN that routes the row to another import, and a credit naming
// nobody this catalogue can identify (refuseLibexCredits, the libex rule) - and
// leaves a missing title, author, narrator or language to addBook, which owns
// those rules for every source.
func (sp *storeParse) parseRow(e rawBook) {
	store, link := strings.TrimSpace(e.str("store")), strings.TrimSpace(e.str("url"))
	label := firstNonEmpty(link, e.str("title"), "(unknown row)")
	switch {
	case !model.KnownStore(store):
		sp.refuse(label, "store %q is not one of %s", store, strings.Join(model.StoreKeys(), ", "))
		return
	case !model.StoreLinkOK(store, link):
		sp.refuse(label, "url %q is not %s", link, model.StoreLinkRule(store))
		return
	case e.str("asin") != "":
		sp.refuse(label, "it carries an ASIN (%s); a listing with an ASIN is imported through `metaimport libex`", e.str("asin"))
		return
	}
	authors, narrators := libexNames(e["authors"]), libexNames(e["narrators"])
	if r, refused := refuseLibexCredits(authors, narrators); refused {
		sp.refuse(label, "%s", r.detail)
		return
	}
	sp.books = append(sp.books, sp.storeToBook(e, store, link, authors, narrators))
}

// storeToBook normalizes one store row into a sourceBook: the facts a libex
// row and a store row share (libexSharedFacts), through the keys addBook reads.
func (sp *storeParse) storeToBook(e rawBook, store, link string, authors, narrators []string) sourceBook {
	sb := sourceBook{raw: rawBook{}, store: &storeFacts{key: store, url: link}}
	sb.authors, sb.narrators = authors, narrators
	libexSharedFacts(e, &sb, func(img string) {
		sp.warnings = append(sp.warnings, fmt.Sprintf("store: %s: cover URL %q is not https; dropped", link, img))
	})
	return sb
}

// dedupeByStoreLink is a store row's dedup gate, the twin of dedupeByASIN: a
// row whose page the catalogue (or this run) already holds is a skip.
func (p *planner) dedupeByStoreLink(b sourceBook) bool {
	if b.store == nil || !p.hasStoreLink(b.store.url) {
		return false
	}
	p.summary.Skipped++
	return true
}

// noteStoreLinks registers every store page a catalogued recording states, so
// a rerun of the same file is a set of skips (loadExisting).
func (p *planner) noteStoreLinks(links *model.RecordingLinks) {
	for _, l := range links.StoreLinks() {
		p.noteStoreLink(l)
	}
}

// noteStoreLink and hasStoreLink are the one place the dedup set is keyed
// (model.StoreLinkKey, pkg/check's uniqueness key).
func (p *planner) noteStoreLink(link string)     { p.storeLinks[model.StoreLinkKey(link)] = true }
func (p *planner) hasStoreLink(link string) bool { return p.storeLinks[model.StoreLinkKey(link)] }

// applyStoreLink writes a store row's page onto the recording it creates and
// registers it, so a later row of the run naming the same page is a skip.
func (p *planner) applyStoreLink(rec *outRecording, b sourceBook) {
	if b.store == nil {
		return
	}
	if rec.Links == nil {
		rec.Links = &model.RecordingLinks{}
	}
	rec.Links.SetStore(b.store.key, b.store.url)
	p.noteStoreLink(b.store.url)
}

// storeLanding is what landStoreLink did with a store row among the work's
// same-narrator recordings.
type storeLanding int

const (
	storeNoProduction storeLanding = iota // no same-narrator retail sibling: the row is a recording of its own
	storeLinked                           // the page was added to the one sibling that is this production
	storeRefused                          // a sibling exists but none, or more than one, is provably this production (or it states another page)
)

// landStoreLink is addRecording's store branch over the work's same-narrator
// recordings (matches). A store row carries no identifier, so like the ISBN
// attach (attachRowISBNs) it adds its page to a held recording only on PROOF
// that the recording is its production, and never to "the first match":
//
//   - LibriVox projects are set aside first (never a retail listing's
//     production); with none left the row is a recording of its own;
//   - a held recording QUALIFIES when both it and the row state a runtime, the
//     two are compatible (sameRuntime), the abridged flags do not conflict and
//     no series position contradicts the row's claim (narrators already match);
//   - exactly ONE qualifying recording gains the page, when it states none for
//     that store (the existing value wins: one stating another page is refused);
//   - zero or several qualifying is a refusal with a warning naming why: a
//     refused row is re-importable, a page on the wrong production or a twin
//     recording of a held one is not.
func (p *planner) landStoreLink(ws *workState, matches []recCandidate, b sourceBook, claims []posClaim, narrSet map[string]bool, warn func(string, ...any)) storeLanding {
	prod := resolvedRowProduction(b, narrSet)
	var retail, qualified []recCandidate
	var why []string
	for _, m := range matches {
		if m.info.librivox {
			continue
		}
		retail = append(retail, m)
		known, row := m.info.known, b.evidence
		switch {
		case known.min <= 0 || row.min <= 0:
			why = append(why, fmt.Sprintf("recording %q and this row do not both state a runtime", m.slug))
		case !sameRuntime(known, row):
			why = append(why, fmt.Sprintf("recording %q runs %d minutes and this row %d", m.slug, known.min, row.min))
		case abridgedConflict(m.info.abridged, b.abridged):
			why = append(why, fmt.Sprintf("recording %q and this row disagree on abridged", m.slug))
		default:
			if series, incumbent, want, conflict := p.seriesPosConflict(m.info, ws, claims, prod); conflict {
				why = append(why, fmt.Sprintf("recording %q is at position %q of series %q and this row claims %q", m.slug, incumbent, series, want))
				continue
			}
			qualified = append(qualified, m)
		}
	}
	switch {
	case len(retail) == 0:
		return storeNoProduction
	case len(qualified) > 1:
		warn("%d same-narrator recordings could be this row's production; its store page is not added and no recording is created", len(qualified))
		return storeRefused
	case len(qualified) == 0:
		warn("%s; it is not proven to be that production, so its store page is not added and no recording is created", strings.Join(why, "; "))
		return storeRefused
	}
	m := qualified[0]
	ref := RecRef{Work: ws.slug, Rec: m.slug}
	entry, raw := p.recordingRaw(ref.Work, ref.Rec)
	if raw == nil {
		return storeRefused
	}
	links, _ := raw["links"].(map[string]any)
	if have, _ := links[b.store.key].(string); have != "" {
		warn("recording %s is this production and already states links.%s %q; not replacing it with %q",
			recLabel(ref.Work, ref.Rec), b.store.key, have, b.store.url)
		return storeRefused
	}
	if links == nil {
		links = map[string]any{}
	}
	links[b.store.key] = b.store.url
	raw["links"] = links
	p.commitRecording(ref, entry, raw)
	// The stamp is a dated bulk-mirror statement, which can end a preorder
	// estimate's state (runtimeEstimated), so a later row of the run is
	// compared with what the record now says (as mergeRecordingASIN does).
	syncRecInfo(m.info, raw)
	p.noteStoreLink(b.store.url)
	p.landedRec = m.slug
	p.summary.StoreLinksAdded++
	return storeLinked
}
