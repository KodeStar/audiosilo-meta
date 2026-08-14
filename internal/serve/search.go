package serve

import (
	"strings"
	"unicode"
)

// workResult is a search hit that is a work: the card fields inline, plus the
// kind discriminator and the work's narrators.
type workResult struct {
	Kind      searchKind  `json:"kind"`
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Authors   []personRef `json:"authors"`
	Series    *seriesRef  `json:"series"`
	CoverURL  *string     `json:"cover_url"`
	AddedAt   *string     `json:"added_at"`
	Narrators []personRef `json:"narrators"`
}

type personResult struct {
	Kind searchKind `json:"kind"`
	ID   string     `json:"id"`
	Name string     `json:"name"`
}

type seriesResult struct {
	Kind  searchKind `json:"kind"`
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Works int        `json:"works"`
}

// ftsQuery turns a raw user query into a safe FTS5 MATCH expression: the query
// is cut into terms (see ftsTerms), each term is wrapped in double quotes so it
// can never be read as an operator, and the final term gets a trailing '*' for
// prefix matching. A query holding no term at all - empty, whitespace, or pure
// punctuation - yields a harmless empty-phrase match rather than a syntax error.
func ftsQuery(q string) string { return ftsMatch(q, true) }

// ftsPhrase is ftsQuery without the trailing prefix-star: the same terms,
// matching only whole ones. It is what a NAME lookup wants (see seriespos.go);
// a prefix-star over a short query walks a large fraction of the index, which is
// affordable for the one hit the user is typing towards and not for a lookup the
// server issues on their behalf.
func ftsPhrase(q string) string { return ftsMatch(q, false) }

// ftsMatch is the single escaping implementation behind both: it is the one
// place a term becomes an FTS5 phrase, so no caller can ever build a MATCH
// expression a quote or an operator could break.
//
// Each term is its OWN one-word phrase, which is the whole point of ftsTerms:
// several words inside one phrase are an ADJACENCY constraint, and that is what
// made punctuated queries return nothing (see ftsTerms). The quote doubling is
// FTS5's own escaping and is now a backstop rather than a live path - a term
// holds only term runes, so it cannot contain a quote - kept so that widening
// ftsTerms' rune rule can never turn into a syntax error.
func ftsMatch(q string, prefixLast bool) string {
	terms := ftsTerms(q)
	if len(terms) == 0 {
		return `""`
	}
	parts := make([]string, len(terms))
	for i, term := range terms {
		escaped := strings.ReplaceAll(term, `"`, `""`)
		parts[i] = `"` + escaped + `"`
		if prefixLast && i == len(terms)-1 {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " ")
}

// ftsTerms cuts a raw query into the words the FTS5 tokenizer would keep: a term
// is a maximal run of letters, digits and combining marks, and every other rune
// is a boundary. It is the fix for punctuated queries.
//
// THE BUG. The predecessor split on WHITESPACE only (strings.Fields), so a token
// carrying interior punctuation became ONE quoted phrase. FTS5 tokenizes a
// quoted string with the same unicode61 tokenizer that indexed the row, so the
// punctuation itself was never the problem - what survived it was: the words
// stayed a MULTI-TERM PHRASE, which FTS5 matches only where those words appear
// consecutively, in that order, inside ONE column. Punctuation a user (or a
// filename) writes INSTEAD of a space is not an adjacency claim, so the
// constraint could not be met and the query returned zero rows for a work the
// catalogue holds:
//
//	q=Greg.Bear-Halo.Primordium  ->  "Greg.Bear-Halo.Primordium"*  ->  0 hits
//	                                 (author is in `names`, title in `title`;
//	                                  words in two columns are never adjacent)
//	q=Primordium,Halo            ->  "Primordium,Halo"*            ->  0 hits
//	                                 (the row has them the other way round)
//
// Cutting the same queries into one phrase per word returns Halo: Primordium,
// which is what every client had to strip punctuation by hand to get.
//
// The rule also drops a punctuation-only token ("&", "-", ":") instead of
// emitting the empty phrase FTS5 would only tolerate by ignoring it, and it puts
// ftsQuery's prefix-star on the last real word rather than on the last fragment
// of a welded phrase.
//
// IT COSTS NOTHING EXTRA. A phrase of n words and n one-word phrases read the
// same n posting lists; the phrase only adds a position check on top. So this
// widens what MATCHES (adjacency is no longer required) without widening what
// the index WALKS, and the probes' cost gates (worthProbing,
// worthTitleProbing - both reading whitespace tokens, deliberately untouched)
// still bound which queries reach an FTS probe at all.
//
// The runs are returned as SUBSTRINGS of q, never rewritten, so each one
// tokenizes to exactly what FTS5 would have made of it in place. Combining
// marks count as term runes for that reason: a decomposed "Ma<combining
// diaeresis>dchen" is one token to unicode61 (which strips the diacritic), and
// splitting on the mark would have asked for two.
func ftsTerms(q string) []string {
	var terms []string
	start := -1
	for i, r := range q {
		if isTermRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			terms = append(terms, q[start:i])
			start = -1
		}
	}
	if start >= 0 {
		terms = append(terms, q[start:])
	}
	return terms
}

// isTermRune reports whether r belongs inside a term: a letter, a digit or a
// combining mark. It is this package's approximation of unicode61's
// alphanumeric class - everything else is a token boundary there too - and the
// one place the boundary rule is spelled.
func isTermRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// searchHit is one row of the FTS result, kept in rank order while the work
// cards are resolved in a single batch.
type searchHit struct {
	kind searchKind
	id   string
}

// mergeHits puts the boosted work ids in front of the FTS hits, drops the
// duplicate an FTS hit would be, and truncates to the page size. The page stays
// exactly limit long, so a boost costs the last FTS hit rather than widening the
// response.
//
// It is the ONE place search results are deduped, so no producer needs its own
// seen set - which is what lets boostedWorks simply concatenate two probes'
// answers, the same work resolved twice collapsing to its first, best-reasoned
// position. Prepending is only sound because /search returns a single ranked
// page with no offset: there is no later page for a boost to shift a hit onto.
// If the endpoint ever grows ?offset, the boost has to apply at offset 0 only.
func mergeHits(boosted []string, ftsHits []searchHit, limit int) []searchHit {
	if len(boosted) == 0 {
		return ftsHits
	}
	out := make([]searchHit, 0, len(boosted)+len(ftsHits))
	seen := make(map[searchHit]bool, len(boosted))
	for _, id := range boosted {
		h := searchHit{kind: kindWork, id: id}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range ftsHits {
		if !seen[h] {
			out = append(out, h)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// searchKind is a value of search_fts's kind column: the three families the
// index holds, plus kindAny for the unscoped search. The column is stored but
// UNINDEXED, so a type-scoped search can filter on it in the WHERE - no new
// index and no artifact change, which is what makes the scoped endpoints work
// against every already-published release.
//
// It is also the JSON type of every result's `kind` discriminator, so the value
// a handler is scoped to and the value it emits are the same constant. A defined
// string type marshals exactly as the string it is, so the wire bytes are
// unchanged.
type searchKind string

const (
	// kindAny is the unscoped search: no kind predicate at all, every family.
	// It is deliberately the empty string, so the zero value is the widest
	// query rather than an accidental scope.
	kindAny    searchKind = ""
	kindWork   searchKind = "work"
	kindPerson searchKind = "person"
	kindSeries searchKind = "series"
)

// The two FTS queries behind every search endpoint. They differ only in the kind
// predicate, so a scoped page is filtered at the source rather than after the
// fact: ?limit=20 on works/search returns 20 works, not the works among 20 mixed
// hits. ftsHits is the one place either is issued, so the SQL and its arguments
// are chosen together and can never be paired wrongly.
const (
	searchSQL     = `SELECT kind, id FROM search_fts WHERE search_fts MATCH ? ORDER BY bm25(search_fts) LIMIT ?`
	searchKindSQL = `SELECT kind, id FROM search_fts WHERE search_fts MATCH ? AND kind = ? ORDER BY bm25(search_fts) LIMIT ?`
)

// workIDsInFTSSQL is the id-only kind-scoped subquery the coverage browser
// narrows with. The kind literal is composed from the constant at COMPILE time,
// so the vocabulary has one home and the string stays a constant the index guard
// can EXPLAIN.
const workIDsInFTSSQL = `SELECT id FROM search_fts WHERE search_fts MATCH ? AND kind='` + string(kindWork) + `'`

// search runs the FTS query for one kind - kindAny for the combined endpoint,
// kindWork/kindPerson/kindSeries for the type-scoped ones - and assembles the
// hits into their per-kind result shapes. The kind travels as DATA, so the four
// endpoints share one implementation and one page composition.
//
// Two query-side boosts run ahead of the FTS hits, both resolving works the
// query names outright: a query that IS a work's title returns that work first
// (exacttitle.go), and a query that names a series and a number ("jack reacher
// 2") resolves that volume (seriespos.go). See those files for why the
// resolution is query-side rather than extra text in the index. The boosts are
// additive: the FTS hits that follow are exactly the ones the query returned
// before, minus any duplicate of a boosted work, and a query that resolves
// neither is byte-for-byte the old behaviour.
//
// Both apply to the combined page and to the WORK scope only. The ids they
// resolve are always works, so prepending them on a people or series page would
// put a work where the endpoint promises neither.
func (s *snapshot) search(kind searchKind, q string, limit int) ([]any, error) {
	hits, err := s.ftsHits(kind, ftsQuery(q), limit)
	if err != nil {
		return nil, err
	}
	var boosted []string
	if kind == kindAny || kind == kindWork {
		boosted = s.boostedWorks(q)
	}
	return s.results(mergeHits(boosted, hits, limit))
}

// ftsHits runs the search query for kind and materializes its rows in rank
// order. It picks the SQL constant and builds that constant's arguments in the
// same breath, so the pairing is structural rather than a convention two call
// sites have to remember.
//
// The hits are collected FIRST so the whole page's cards, names and summaries
// can be resolved in one batch each (see results) rather than inside the scan
// loop.
func (s *snapshot) ftsHits(kind searchKind, match string, limit int) ([]searchHit, error) {
	query, args := searchSQL, []any{match, limit}
	if kind != kindAny {
		query, args = searchKindSQL, []any{match, string(kind), limit}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanPairs(rows, func(kind, id string) searchHit {
		return searchHit{kind: searchKind(kind), id: id}
	})
}

// boostedWorks resolves the work ids a query names outright: the works whose
// TITLE it is (exacttitle.go), then the volume a "<series> <n>" reading resolves
// (seriespos.go).
//
// The exact title leads. It is an EQUALITY on a fact the record states, while
// the series-position hit is an inference - a parse of the query plus a fuzzy
// name probe - so when both fire the literal reading of what the user typed
// comes first. Usually they agree and mergeHits collapses them: a work titled
// "Halo 2" that is also volume 2 of Halo is one id from both probes.
//
// A failed probe degrades to "no boost" instead of failing the request: the
// boost is an enrichment of a page the FTS query produces on its own, and a 500
// on a search the user could otherwise have had is the worse outcome. Each probe
// is judged on its own, so one broken probe does not cost the other's answer,
// and both are logged rather than silent.
func (s *snapshot) boostedWorks(q string) []string {
	titles, err := s.exactTitleHits(q)
	if err != nil {
		s.logf("serve: exact-title probe for %q failed, serving the plain search page: %v", q, err)
	}
	positions, err := s.seriesPositionHits(q)
	if err != nil {
		s.logf("serve: series-position probe for %q failed, serving the plain search page: %v", q, err)
	}
	return append(titles, positions...)
}

// results composes one ranked page of hits into their per-kind result shapes.
// It is the single assembly every search endpoint shares, so the composition
// rules of a result cannot be spelled twice and drift apart.
//
// EVERY per-hit read is batched over the ids the page actually holds - work
// cards (cardsByID), work narrators, person names, series summaries - rather
// than issued inside the loop. These are the busiest endpoints in the API (every
// keystroke of the site's search box) and a type-scoped page is 100% one kind,
// so a per-hit query there is a whole page of sequential round-trips, not the
// occasional one a mixed page would pay.
func (s *snapshot) results(hits []searchHit) ([]any, error) {
	var workIDs, personIDs, seriesIDs []string
	for _, h := range hits {
		switch h.kind {
		case kindWork:
			workIDs = append(workIDs, h.id)
		case kindPerson:
			personIDs = append(personIDs, h.id)
		case kindSeries:
			seriesIDs = append(seriesIDs, h.id)
		}
	}
	cards, err := s.cardsByID(workIDs)
	if err != nil {
		return nil, err
	}
	narrators, err := s.narratorsByWork(workIDs)
	if err != nil {
		return nil, err
	}
	names, err := s.namesByPersonID(personIDs)
	if err != nil {
		return nil, err
	}
	summaries, err := s.seriesSummariesByID(seriesIDs)
	if err != nil {
		return nil, err
	}

	// A hit the batch could not resolve is dropped rather than emitted half
	// composed: the FTS index and the tables it points at come out of one build,
	// so a miss means an inconsistent artifact, and a page missing one row beats
	// a result with a blank name.
	out := []any{}
	for _, h := range hits {
		switch h.kind {
		case kindWork:
			card := cards[h.id]
			if card == nil {
				continue
			}
			// A work with no narrators is absent from the batch, but the wire
			// contract says narrators is always an array.
			ns := narrators[h.id]
			if ns == nil {
				ns = []personRef{}
			}
			out = append(out, workResult{
				Kind: kindWork, ID: card.ID, Title: card.Title, Authors: card.Authors,
				Series: card.Series, CoverURL: card.CoverURL, AddedAt: card.AddedAt,
				Narrators: ns,
			})
		case kindPerson:
			name, ok := names[h.id]
			if !ok {
				continue
			}
			out = append(out, personResult{Kind: kindPerson, ID: h.id, Name: name})
		case kindSeries:
			sum, ok := summaries[h.id]
			if !ok {
				continue
			}
			out = append(out, seriesResult{Kind: kindSeries, ID: h.id, Name: sum.name, Works: sum.works})
		}
	}
	return out, nil
}
