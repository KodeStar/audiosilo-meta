package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// libexselect.go is the BOUNDED-SUBSET selector that stands between libex's
// ~1.1M-row dump and `metaimport libex`. LICENSING.md's import posture is that
// this project is never a mirror of a retailer database: it imports curated,
// maintainer-reviewed tranches. This tool is how a tranche is chosen
// mechanically instead of by hand - it selects the rows that COMPLETE series
// the catalogue already tracks, and refuses everything else.
//
// It is a pure selection pass: it decides which rows to keep and re-emits them
// VERBATIM as NDJSON for the normal create path (`metaimport libex
// subset.ndjson`). It never reshapes a row, never writes into data/, and never
// invents a fact - a reshaping selector would silently become a second,
// untested mapping layer.
//
// Rows stream, but two structures grow with the input and both bounds are worth
// stating plainly: the SELECTED rows are held in full (that is what the
// series-completion bound buys - a tranche, not a dump), and the within-export
// ASIN dedup set holds one entry per DISTINCT ASIN READ, which is the whole
// export (~115MB measured over the 1.06M-row dump). A selection run is
// therefore a few hundred MB, not a few GB - bounded, but not row-count-free.
//
// Every excluded row is counted under the first rule it failed, so the report
// accounts for every row read. Nothing is truncated silently: the per-series cap
// prints exactly what it cut.
//
// Deliberately NOT implemented: a --top popularity flag. The dump carries no
// ratings-count column, so any "top N" would be a guess dressed as a fact.

// SelectOptions configures a libex subset-selection run.
type SelectOptions struct {
	// DataDir is the data root the selection is made against (its series are
	// the completion targets, its ASINs the already-present set).
	DataDir string
	// MaxPerSeries caps how many NEW distinct works may be selected per
	// catalogue series. 0 means unlimited.
	MaxPerSeries int
}

// SelectReasonAlreadyPresent and its siblings name the rules a row can fail.
// A row is counted under the FIRST rule it fails, in the order the constants
// are listed here (which is the order selectLibexRow applies them), so the
// exclusion counts partition the rows read.
const (
	reasonNoASIN        = "malformed or missing ASIN"
	reasonAlreadyASIN   = "ASIN already in the catalogue"
	reasonDuplicateASIN = "duplicate ASIN within the export"
	reasonNoSeries      = "no catalogue series"
	reasonNoPosition    = "series position missing or unparseable"
	reasonLanguage      = "unmapped language"
	reasonRegion        = "unmapped region"
	reasonAINarrator    = "narrated by an AI voice"
	reasonJunkCredit    = "a credited name is a platform account"
	reasonListCredit    = "a credited name is a list of people"
	reasonPlaceholder   = "a credited name is a cast placeholder"
	reasonUnnamedCredit = "a credited name does not identify a person"
	reasonPositionTaken = "series position already claimed"
	reasonSeriesCap     = "over the per-series cap"
)

// reasonOrder is the report order for the exclusion counts (the order the
// rules are applied).
var reasonOrder = []string{
	reasonNoASIN, reasonAlreadyASIN, reasonDuplicateASIN,
	reasonNoSeries, reasonNoPosition, reasonLanguage, reasonRegion,
	reasonAINarrator, reasonJunkCredit, reasonListCredit, reasonPlaceholder, reasonUnnamedCredit,
	reasonPositionTaken, reasonSeriesCap,
}

// SeriesCount is one catalogue series' share of a selection.
type SeriesCount struct {
	Series string // catalogue series slug
	Name   string // catalogue series name
	Rows   int    // rows selected for it
	Works  int    // distinct projected new works selected for it
	// CutWorks / CutRows are what the per-series cap removed. They are always
	// reported (never silently dropped).
	CutWorks int
	CutRows  int
}

// SelectResult is everything a selection run learned, for the report.
type SelectResult struct {
	RowsRead     int
	RowsSelected int
	// SeriesMatched is the number of distinct catalogue series the selected
	// rows belong to.
	SeriesMatched int
	// ProjectedWorks is the number of NEW works the selection would create,
	// counted by distinct (work title slug, catalogue series) - NOT by row, so
	// the per-region sibling rows of one title (which the importer folds into
	// one recording) count once.
	ProjectedWorks int
	// PerSeries is the per-series breakdown, ordered by works desc, then rows
	// desc, then series slug.
	PerSeries []SeriesCount
	// Excluded counts rows per reason (keys are the reason constants).
	Excluded map[string]int
	// Warnings are informational lines (a catalogue that did not fully
	// validate) that do not stop the run. A malformed row is NOT a warning: it
	// is an exclusion, counted like every other one.
	Warnings []string
}

// SelectLibex reads a libex export from exportPath, selects the rows worth
// importing against opts.DataDir, and writes them to outPath as NDJSON (one
// row per line, each row's own JSON passed through verbatim). It returns the
// report either way, but an error means the report covers only the rows the run
// reached and NO output file exists (the write is atomic) - so a caller must not
// present a failed run's report as a tranche.
func SelectLibex(exportPath, outPath string, opts SelectOptions) (SelectResult, error) {
	if err := refuseSelfOverwrite(exportPath, outPath); err != nil {
		return SelectResult{}, err
	}
	in, err := os.Open(exportPath) //nolint:gosec // an operator-supplied export path is the whole point of the tool
	if err != nil {
		return SelectResult{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	defer func() { _ = in.Close() }()

	res, rows, err := selectLibexRows(in, opts)
	if err != nil {
		return res, err
	}
	if err := writeNDJSON(outPath, rows); err != nil {
		return res, err
	}
	return res, nil
}

// refuseSelfOverwrite fails when the output would land on the input export.
// The subset is always a strict reduction of its input, so this can only ever
// be a mistake - and the mistake it guards is destructive: `-o subset.ndjson
// full.ndjson` under a naive argument split once read subset.ndjson as the
// input and truncated the operator's multi-GB dump to nothing, reporting
// success. Distinct names can still be one file, so a symlink or hard link is
// caught too.
func refuseSelfOverwrite(exportPath, outPath string) error {
	same := filepath.Clean(exportPath) == filepath.Clean(outPath)
	if !same {
		inAbs, inErr := filepath.Abs(exportPath)
		outAbs, outErr := filepath.Abs(outPath)
		same = inErr == nil && outErr == nil && inAbs == outAbs
	}
	if !same {
		inInfo, inErr := os.Stat(exportPath)
		outInfo, outErr := os.Stat(outPath)
		same = inErr == nil && outErr == nil && os.SameFile(inInfo, outInfo)
	}
	switch {
	case same && outPath == exportPath:
		return fmt.Errorf("refusing to write the subset over the input export: -o names the same file (%s)", exportPath)
	case same:
		return fmt.Errorf("refusing to write the subset over the input export: -o %s and %s are the same file", outPath, exportPath)
	}
	return nil
}

// selectedRow is a kept row plus the facts the cap and the report need. raw is
// the row's own JSON bytes, never re-marshalled from the decoded map: the
// output of a selection pass must be the input row, not this package's
// rendering of it.
//
// Every kept row carries a parseable series position (the completion rules
// refuse the rest), so pos is always meaningful and the cap needs no
// "unpositioned" tiebreak.
type selectedRow struct {
	raw        []byte
	seriesSlug string
	workKey    string
	pos        float64
}

// selectState is the within-export memory the per-row rules keep: the ASINs
// already seen, and the (series, position) slots already claimed by a selected
// row. Both are first-seen-wins, so a run is deterministic in input order.
type selectState struct {
	seenASIN map[string]bool
	// claimed maps "<series slug>\x00<position>" to the work key holding it.
	// The value matters because the per-region sibling rows of ONE title
	// legitimately claim the same slot - they are one work.
	claimed map[string]string
}

func newSelectState() *selectState {
	return &selectState{seenASIN: map[string]bool{}, claimed: map[string]string{}}
}

// claimPosition reserves (series, position) for workKey, reporting false when
// something else already holds it.
//
// A slot is unavailable either because the CATALOGUE's series entry already
// records a work there, or because an earlier row of this run claimed it for a
// different work (the two DE-sibling volumes libex lists at one position). The
// importer's addToSeries refuses the loser in both cases and creates the work
// ANYWAY, orphaned outside the series - which is exactly what a series
// COMPLETION must not produce. A taken position is enrichment fodder, not a
// completion, so the row is excluded and reported instead.
func (st *selectState) claimPosition(idx seriesIndex, slug, seq, workKey string) bool {
	if idx.positions[slug][seq] != "" {
		return false
	}
	key := slug + "\x00" + seq
	if owner, taken := st.claimed[key]; taken {
		return owner == workKey
	}
	st.claimed[key] = workKey
	return true
}

// selectLibexRows streams the export, applies the selection rules, and returns
// the report plus the kept rows in INPUT order (so the per-region sibling rows
// the export SQL made adjacent stay adjacent for the importer's batch pre-pass).
func selectLibexRows(r io.Reader, opts SelectOptions) (SelectResult, []selectedRow, error) {
	res := SelectResult{Excluded: map[string]int{}}
	idx, warnings := loadSeriesIndex(opts.DataDir)
	res.Warnings = append(res.Warnings, warnings...)

	st := newSelectState()
	var kept []selectedRow

	err := streamLibexRows(r, func(raw []byte, e rawBook) {
		res.RowsRead++
		row, reason := selectLibexRow(e, idx, st)
		if reason != "" {
			res.Excluded[reason]++
			return
		}
		row.raw = raw
		kept = append(kept, row)
	})
	if err != nil {
		return res, nil, err
	}

	kept, cuts := applySeriesCap(kept, opts.MaxPerSeries, &res)
	summarize(kept, cuts, idx, &res)
	return res, kept, nil
}

// selectLibexRow applies the per-row rules to one decoded row, returning the
// kept row or the first reason it was excluded. st carries the within-export
// dedup set and position claims, and is updated as the rules pass.
//
// The rules are ordered so the counts read usefully: identity first (a row we
// cannot address, or already have), then the completion test that defines the
// tranche, and only then the mapping and credit tests - so "unmapped language"
// counts rows we actually wanted, not the 99 percent of the dump that was never
// in scope. The position claim comes last because it MUTATES state: only a row
// that would otherwise be kept may reserve a slot.
func selectLibexRow(e rawBook, idx seriesIndex, st *selectState) (selectedRow, string) {
	asin := NormalizeASIN(e.str("asin"))
	if asin == "" {
		return selectedRow{}, reasonNoASIN
	}
	if idx.asins[asin] {
		return selectedRow{}, reasonAlreadyASIN
	}
	if st.seenASIN[asin] {
		return selectedRow{}, reasonDuplicateASIN
	}
	st.seenASIN[asin] = true

	slug, ref, ok := idx.match(libexSeries(e["series"]))
	if !ok {
		return selectedRow{}, reasonNoSeries
	}
	// A row that names a series but no usable position in it is not a
	// completion: the importer would create the work and then warn that it
	// could not be placed, leaving an orphan work outside the series it was
	// selected to complete.
	pos, posOK := seriesPositionValue(ref)
	if !posOK {
		return selectedRow{}, reasonNoPosition
	}
	if _, langOK := mapLanguage(e.str("language")); !langOK {
		return selectedRow{}, reasonLanguage
	}
	if _, _, regionOK := libexRegion(e); !regionOK {
		return selectedRow{}, reasonRegion
	}
	// The credit-side refusals the parse layer applies (refuseLibexCredits). A
	// row the importer will refuse must not be selected: it would be counted as a
	// completion the tranche does not actually deliver, and - because the position
	// claim below is first-seen-wins - it would take the slot away from a sibling
	// row that IS importable.
	if r, refused := refuseLibexCredits(libexNames(e["authors"]), libexNames(e["narrators"])); refused {
		return selectedRow{}, r.reason
	}

	// The work key is the importer's own work identity as far as a selection
	// pass can know it: the slug of the work title (libex's "title", which
	// libexToBook carries as title_short) within the matched series. Two
	// per-region sibling rows of one title therefore share a key and project
	// ONE new work, which is what the importer's same-narrator ASIN merge
	// actually does with them.
	workKey := slug + "\x00" + Slugify(e.str("title"))
	if !st.claimPosition(idx, slug, ref.seq, workKey) {
		return selectedRow{}, reasonPositionTaken
	}
	return selectedRow{seriesSlug: slug, workKey: workKey, pos: pos}, ""
}

// seriesPositionValue reduces a matched series claim to the numeric value the
// cap orders by. An omnibus range ("1-3.5") sorts by its first volume. A claim
// the shared position rules rejected, or one whose leading token is not a
// number, has no usable position at all.
func seriesPositionValue(ref seriesRef) (float64, bool) {
	if !ref.seqOK {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.SplitN(ref.seq, "-", 2)[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// applySeriesCap keeps at most maxPerSeries distinct NEW works per catalogue
// series, in series-position order (ties in first-seen order). Whole works are
// cut, never individual rows of one - a half-selected title would import as a
// work missing a region's ASIN. The cut is returned per series (and counted as
// an exclusion in res), so the cap is never a silent truncation.
func applySeriesCap(rows []selectedRow, maxPerSeries int, res *SelectResult) ([]selectedRow, map[string]SeriesCount) {
	if maxPerSeries <= 0 {
		return rows, nil
	}
	type workEntry struct {
		key   string
		pos   float64
		order int
		rows  int
	}
	bySeries := map[string][]*workEntry{}
	byKey := map[string]*workEntry{}
	for i, row := range rows {
		w, ok := byKey[row.workKey]
		if !ok {
			w = &workEntry{key: row.workKey, pos: row.pos, order: i}
			byKey[row.workKey] = w
			bySeries[row.seriesSlug] = append(bySeries[row.seriesSlug], w)
		}
		w.rows++
	}

	cut := map[string]bool{}
	cutBySeries := map[string]SeriesCount{}
	for slug, works := range bySeries {
		if len(works) <= maxPerSeries {
			continue
		}
		sort.SliceStable(works, func(i, j int) bool {
			a, b := works[i], works[j]
			if a.pos != b.pos {
				return a.pos < b.pos
			}
			return a.order < b.order
		})
		tally := SeriesCount{Series: slug}
		for _, w := range works[maxPerSeries:] {
			cut[w.key] = true
			tally.CutWorks++
			tally.CutRows += w.rows
		}
		cutBySeries[slug] = tally
	}
	if len(cut) == 0 {
		return rows, nil
	}

	out := rows[:0]
	for _, row := range rows {
		if cut[row.workKey] {
			res.Excluded[reasonSeriesCap]++
			continue
		}
		out = append(out, row)
	}
	return out, cutBySeries
}

// summarize fills the selection totals and the per-series breakdown from the
// finally-kept rows and the cap's cuts.
func summarize(rows []selectedRow, cuts map[string]SeriesCount, idx seriesIndex, res *SelectResult) {
	res.RowsSelected = len(rows)
	tallies := map[string]*SeriesCount{}
	works := map[string]bool{}
	for _, row := range rows {
		t := tallies[row.seriesSlug]
		if t == nil {
			t = &SeriesCount{Series: row.seriesSlug, Name: idx.names[row.seriesSlug]}
			tallies[row.seriesSlug] = t
		}
		t.Rows++
		if !works[row.workKey] {
			works[row.workKey] = true
			t.Works++
		}
	}
	// The cap only ever cuts a series down to maxPerSeries (>= 1) works, so
	// every cut series still has kept rows and therefore a tally already.
	for slug, cut := range cuts {
		t := tallies[slug]
		t.CutWorks, t.CutRows = cut.CutWorks, cut.CutRows
	}
	res.ProjectedWorks = len(works)
	// A tally exists only because a row was kept for it, and a kept row always
	// contributes a work, so every listed series is a matched one.
	res.SeriesMatched = len(tallies)
	res.PerSeries = make([]SeriesCount, 0, len(tallies))
	for _, t := range tallies {
		res.PerSeries = append(res.PerSeries, *t)
	}
	sort.Slice(res.PerSeries, func(i, j int) bool {
		a, b := res.PerSeries[i], res.PerSeries[j]
		switch {
		case a.Works != b.Works:
			return a.Works > b.Works
		case a.Rows != b.Rows:
			return a.Rows > b.Rows
		default:
			return a.Series < b.Series
		}
	})
}

// seriesIndex is the catalogue view a selection needs: the series a row's name
// can complete, and the ASINs already recorded.
type seriesIndex struct {
	bySlug map[string]string // slug -> series name
	names  map[string]string // same map, read under its reporting name
	asins  map[string]bool
	// positions maps a series slug to the positions its works already occupy
	// (position -> work id). A position already taken cannot be completed into.
	positions map[string]map[string]string
}

// loadSeriesIndex reads the catalogue at dataDir. A tree with validation
// problems is still used (best-effort, exactly like the importer's
// loadExisting) but is warned about: selecting against a half-loaded catalogue
// would silently re-import books that are already there.
func loadSeriesIndex(dataDir string) (seriesIndex, []string) {
	idx := seriesIndex{
		bySlug:    map[string]string{},
		asins:     map[string]bool{},
		positions: map[string]map[string]string{},
	}
	idx.names = idx.bySlug
	res := check.Load(dataDir)
	var warnings []string
	if !res.OK() {
		warnings = append(warnings, fmt.Sprintf("catalogue at %s has %d validation problem(s); selecting against it best-effort", dataDir, len(res.Problems)))
	}
	if res.Catalog == nil {
		return idx, warnings
	}
	for _, s := range res.Catalog.Series {
		idx.bySlug[s.ID] = s.Name
		taken := make(map[string]string, len(s.Works))
		for _, sw := range s.Works {
			// Compare positions in the same canonical spelling a row's claim
			// arrives in, so a stored "1.0" and a claimed "1" are one slot.
			pos := sw.Position
			if norm, ok := NormalizeSequence(pos); ok {
				pos = norm
			}
			taken[pos] = sw.Work
		}
		idx.positions[s.ID] = taken
	}
	for _, w := range res.Catalog.Works {
		for _, rec := range w.Recordings {
			for _, a := range rec.ASIN {
				idx.asins[a.ASIN] = true
			}
		}
	}
	return idx, warnings
}

// match resolves a row's series claims against the catalogue, returning the
// catalogue slug of the first claim that names a series already in the tree,
// together with that claim (the caller reads its position).
func (idx seriesIndex) match(refs []seriesRef) (slug string, matched seriesRef, ok bool) {
	for _, ref := range refs {
		if s, found := idx.find(ref.name); found {
			return s, ref, true
		}
	}
	return "", seriesRef{}, false
}

// find resolves a series NAME to the catalogue slug it would import into. It
// walks exactly the candidate chain the importer's findSeries walks - the same
// NumberedSlugAt formula, so the two cannot drift apart - matching on the stored
// name case-insensitively, so a row is judged "completes a series we have" by
// the same rule that will later place it there. Keying on the slug alone would
// wrongly match a numeric-suffix collision between two different-named series.
func (idx seriesIndex) find(name string) (string, bool) {
	base := Slugify(name)
	if base == "" {
		base = "series"
	}
	for i := 0; ; i++ {
		slug := NumberedSlugAt(base, i)
		stored, exists := idx.bySlug[slug]
		if !exists {
			return "", false
		}
		if strings.EqualFold(stored, name) {
			return slug, true
		}
	}
}

// streamLibexRows decodes an export and calls fn for every row, handing over
// BOTH the row's verbatim JSON bytes and its decoded map. It accepts the same
// three shapes parseLibex does (a top-level array, NDJSON, or a wrapper object
// holding the array), but streams rather than materializing the file: the
// intended input here IS libex's full dump.
//
// A lone object is the ambiguous case and is resolved exactly as
// decodeLibexEntries resolves it - the row's own "asin" key decides, and an
// envelope without one is unwrapped rather than silently imported as zero rows.
func streamLibexRows(r io.Reader, fn func(raw []byte, e rawBook)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	if err := skipBOMAndSpace(br); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("parse libex export: empty input")
		}
		return fmt.Errorf("parse libex export: %w", err)
	}
	lead, err := br.Peek(1)
	if err != nil {
		return fmt.Errorf("parse libex export: %w", err)
	}

	dec := json.NewDecoder(br)
	dec.UseNumber()
	switch lead[0] {
	case '[':
		if _, err := dec.Token(); err != nil { // the opening '['
			return fmt.Errorf("parse libex export: %w", err)
		}
		for dec.More() {
			if err := decodeRow(dec, fn); err != nil {
				return err
			}
		}
		// The loop ends at ']' OR at EOF, and a stream that simply stopped is a
		// TRUNCATED export - so the closing bracket has to be consumed and the
		// stream checked for more, exactly as decodeEntries checks the file it
		// holds in memory. Without this, "[r1,r2" imported cleanly and
		// "[r1][r2]" imported only its first half, both reporting success.
		if _, err := dec.Token(); err != nil { // the closing ']'
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("parse libex export: %w", err)
		}
		if dec.More() {
			return errors.New("parse libex export: trailing content after the first JSON value (concatenated exports?)")
		}
		return nil
	case '{':
		return streamLibexObjects(dec, fn)
	default:
		return errors.New("parse libex export: expected a JSON array of rows, NDJSON, or a wrapper object holding an array")
	}
}

// streamLibexObjects consumes a stream of top-level objects (NDJSON, or a
// single wrapper/row object). Only the FIRST object can be a wrapper, and only
// when it is also the last and carries no "asin" - so the wrapper path buffers
// exactly one object and the NDJSON path buffers none.
func streamLibexObjects(dec *json.Decoder, fn func(raw []byte, e rawBook)) error {
	first := true
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("parse libex export: %w", err)
		}
		entry, err := decodeObjectBytes(raw)
		if err != nil {
			return err
		}
		if first {
			first = false
			if _, isRow := entry["asin"]; !isRow && !dec.More() {
				return streamWrapped(raw, fn)
			}
		}
		fn(raw, entry)
	}
}

// streamWrapped emits the rows of a wrapper object ({"books":[...]}), reusing
// decodeEntries' wrapper-key list AND its refusal wording so the shapes this
// tool accepts and the shapes the importer accepts cannot drift apart. Each
// element is handed over as its own bytes, like every other shape - a wrapper
// is a hand-assembled file rather than the dump, but there is no reason for it
// to be the one shape whose rows get re-rendered.
func streamWrapped(raw json.RawMessage, fn func(raw []byte, e rawBook)) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("parse libex export: %w", err)
	}
	for _, key := range wrapperKeys {
		body, ok := obj[key]
		if !ok {
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err != nil {
			continue
		}
		for _, el := range arr {
			entry, err := decodeRowBytes(el)
			if err != nil {
				return err
			}
			fn(el, entry) // a non-object element rides through as a no-ASIN row
		}
		return nil
	}
	return errNotAnEntryList("libex export")
}

// decodeRow reads the next array element as raw bytes plus a decoded map.
//
// A non-object element decodes to a nil map and is handed over ANYWAY: it fails
// the first rule (no ASIN) and is counted there. Returning early instead made
// it invisible in the array shape while the NDJSON shape counted it, so the
// same file reported different totals depending on how it was spelled. The
// import path drops such an element silently (decodeEntries); this tool
// reports every element it read, which is the stronger property for a step
// whose whole output is a report.
func decodeRow(dec *json.Decoder, fn func(raw []byte, e rawBook)) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("parse libex export: %w", err)
	}
	entry, err := decodeRowBytes(raw)
	if err != nil {
		return err
	}
	fn(raw, entry)
	return nil
}

// decodeObjectBytes decodes one TOP-LEVEL stream value, which must be an
// object. NDJSON carrying anything else is not a file the import path reads
// either - decodeLibexEntries decodes the same stream straight into objects -
// so it is refused here, with json's own wording, rather than counted as an odd
// row. (Inside an ARRAY a non-object element is tolerated by both, see
// decodeRow.)
func decodeObjectBytes(raw []byte) (rawBook, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse libex export: %w", err)
	}
	return rawBook(m), nil
}

// decodeRowBytes decodes one row's JSON into a rawBook, preserving numbers as
// json.Number so the shared coercion helpers behave exactly as they do on the
// import path. A non-object value decodes to a nil map (the caller skips it).
func decodeRowBytes(raw []byte) (rawBook, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse libex export: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil
	}
	return rawBook(m), nil
}

// skipBOMAndSpace advances past a UTF-8 BOM and any leading whitespace, so the
// shape sniff reads the first structural byte.
func skipBOMAndSpace(br *bufio.Reader) error {
	if lead, err := br.Peek(len(utf8BOM)); err == nil && bytes.Equal(lead, utf8BOM) {
		if _, err := br.Discard(len(utf8BOM)); err != nil {
			return err
		}
	}
	for {
		b, err := br.Peek(1)
		if err != nil {
			return err
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			if _, err := br.Discard(1); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// writeNDJSON writes the selected rows, one compacted JSON object per line.
// Each row is its own bytes from the export (only insignificant whitespace is
// removed), so `metaimport libex` sees exactly the facts the dump stated - a
// selection pass must never become a second mapping layer.
//
// The write is atomic (temp file + rename in the destination's directory, the
// repo's convention - see internal/serve installVerified): an interrupted or
// failed run must leave NO subset file rather than a partial one, because a
// truncated NDJSON subset is still a perfectly importable file and would land
// as a silently half-sized tranche.
func writeNDJSON(path string, rows []selectedRow) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".metaimport-subset-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // a no-op once the rename succeeded

	if err := writeRows(tmp, rows); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	// 0600 from CreateTemp would make the subset unreadable to anything but the
	// operator; it is ordinary data, so match the create-mode the plain path had.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeRows compacts each row onto its own line of w.
func writeRows(f io.Writer, rows []selectedRow) error {
	w := bufio.NewWriterSize(f, 1<<20)
	var buf bytes.Buffer
	for _, row := range rows {
		buf.Reset()
		if err := json.Compact(&buf, row.raw); err != nil {
			return err
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}

// topSeries is how many per-series lines the report prints before summarizing
// the tail. The full breakdown stays available on SelectResult.PerSeries.
const topSeries = 20

// Report renders the selection as the operator-facing text block: the totals,
// the per-series breakdown (top 20 plus a total line), and the exclusion
// counts. Every row read is accounted for.
func (r SelectResult) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "selected %d of %d rows: %d projected new works across %d catalogue series\n",
		r.RowsSelected, r.RowsRead, r.ProjectedWorks, r.SeriesMatched)

	shown := min(len(r.PerSeries), topSeries)
	for _, s := range r.PerSeries[:shown] {
		name := s.Name
		if name == "" {
			name = s.Series
		}
		fmt.Fprintf(&b, "  %-40s %3d %-5s %3d %s", trimTo(name, 40), s.Works, plural(s.Works, "work")+",", s.Rows, plural(s.Rows, "row"))
		if s.CutWorks > 0 {
			fmt.Fprintf(&b, " (cap cut %d %s, %d %s)",
				s.CutWorks, plural(s.CutWorks, "work"), s.CutRows, plural(s.CutRows, "row"))
		}
		b.WriteByte('\n')
	}
	if len(r.PerSeries) > shown {
		var works, rows int
		for _, s := range r.PerSeries[shown:] {
			works += s.Works
			rows += s.Rows
		}
		fmt.Fprintf(&b, "  ... %d more series: %d %s, %d %s\n",
			len(r.PerSeries)-shown, works, plural(works, "work"), rows, plural(rows, "row"))
	}
	fmt.Fprintf(&b, "  total: %d series, %d %s, %d %s\n",
		len(r.PerSeries), r.ProjectedWorks, plural(r.ProjectedWorks, "work"),
		r.RowsSelected, plural(r.RowsSelected, "row"))

	fmt.Fprintf(&b, "excluded %d rows:\n", r.RowsRead-r.RowsSelected)
	for _, reason := range reasonOrder {
		fmt.Fprintf(&b, "  %-38s %d\n", reason, r.Excluded[reason])
	}
	if capped := r.cappedSeries(); len(capped) > 0 {
		fmt.Fprintf(&b, "the per-series cap cut works from %d series:\n", len(capped))
		for _, s := range capped {
			name := s.Name
			if name == "" {
				name = s.Series
			}
			fmt.Fprintf(&b, "  %-40s cut %d %s, %d %s\n",
				trimTo(name, 40), s.CutWorks, plural(s.CutWorks, "work"), s.CutRows, plural(s.CutRows, "row"))
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(&b, "  warning:", w)
	}
	return b.String()
}

// cappedSeries lists every series the cap cut from, in report order.
func (r SelectResult) cappedSeries() []SeriesCount {
	var out []SeriesCount
	for _, s := range r.PerSeries {
		if s.CutWorks > 0 {
			out = append(out, s)
		}
	}
	return out
}

// plural renders "1 work" / "2 works" without a separate format string per
// count.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// trimTo shortens a display name to n runes so the report columns line up.
func trimTo(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}
