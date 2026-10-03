package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// libexselect.go is the BOUNDED-SUBSET selector that stands between libex's
// ~1.1M-row dump and `metaimport libex`. LICENSING.md's import posture is that
// this project is never a mirror of a retailer database: it imports curated,
// maintainer-reviewed tranches. This tool is how a tranche is chosen
// mechanically instead of by hand - it selects the rows that COMPLETE series
// the catalogue already tracks, and refuses everything else.
//
// A row is series-completing only when EVERY series claim the importer would act
// on - every claim with a usable position - resolves to a series the catalogue
// already holds. Not the first such claim: the create path places the new work
// in each of those claims and CREATES the series any of them resolved to but the
// catalogue does not hold, so a row claiming one catalogued series and one
// uncatalogued one would mint a series on import. Selecting it broke the
// series-completion bot, whose whole bound is "never a mirror, never a new
// series": it refuses its own pull request when the import would create one. A
// claim the importer merely warns about disqualifies nothing - one with no
// usable position is never placed, and one whose name has no addressable slug
// has no identity to mint.
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
	// AttachEditions (`--attach-editions`) keeps a row whose position the
	// catalogue already fills when it is another edition of the work there
	// (attach.go), for the import's own --attach-editions to attach; without it
	// every such row is refused as position-claimed, as it always was.
	AttachEditions bool
	// RefusalsPath, when set, is where the per-row refusal worklist is written:
	// one {"asin","reason"} line (RowSkip) per refused row, the reason one of
	// RefusalCodes - a contract with the sync bot.
	RefusalsPath string
	// AttachmentsPath, when set, lists the rows kept for ATTACHMENT, one
	// {"asin","work","series","position"} line (Attachment) each: how a caller
	// tells them from completions in the subset, which holds both. A contract
	// with the sync bot like the refusal codes.
	//
	// The subset and both worklists are committed together (atomicfile), the
	// subset last.
	AttachmentsPath string
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

// Attachment is one row selected for attachment to a catalogued work. Its JSON
// form, in this field order, is one --attachments line - a contract with the
// sync bot (SelectOptions.AttachmentsPath).
type Attachment struct {
	ASIN     string `json:"asin"`
	Work     string `json:"work"`     // the incumbent work's slug
	Series   string `json:"series"`   // the catalogue series slug
	Position string `json:"position"` // the position the row and the work share
}

// SelectResult is everything a selection run learned, for the report.
type SelectResult struct {
	RowsRead     int
	RowsSelected int
	// SeriesMatched is the number of distinct catalogue series the selected
	// completion rows belong to.
	SeriesMatched int
	// ProjectedWorks is the number of NEW works the selection would create,
	// counted by distinct (work title slug, catalogue series) - NOT by row, so
	// the per-region sibling rows of one title (which the importer folds into
	// one recording) count once.
	ProjectedWorks int
	// PerSeries is the per-series breakdown, ordered by works desc, then rows
	// desc, then series slug.
	PerSeries []SeriesCount
	// Excluded counts rows per rule, keyed by the rule's report wording.
	Excluded map[string]int
	// Attachments are the selected rows that complete nothing: each claims a
	// position the catalogue already fills and is another edition of the work
	// there (attach.go). They are part of RowsSelected and of no per-series
	// count, in input order.
	Attachments []Attachment
	// UnnamedRefusals counts the refused rows the --refusals worklist could not
	// name because they state no ASIN (still counted under their rule above);
	// always 0 when no worklist was asked for.
	UnnamedRefusals int
	// CutOnly are series the per-series cap cut works from and for which no
	// completion row was finally kept (a later pass of the cap/re-check fixpoint
	// dropped the rest), so they have no PerSeries entry; the report lists them
	// with the other capped series.
	CutOnly []SeriesCount
	// Warnings are informational lines (a catalogue that did not fully
	// validate) that do not stop the run. A malformed row is NOT a warning: it
	// is an exclusion, counted like every other one.
	Warnings []string
}

// SelectLibex reads a libex export from exportPath, selects the rows worth
// importing against opts.DataDir, and writes them to outPath as NDJSON (one
// row per line, each row's own JSON passed through verbatim). It returns the
// report either way, but an error means the report covers only the rows the run
// reached and NO output file was written (atomicfile) - so a caller must not
// present a failed run's report as a tranche.
func SelectLibex(exportPath, outPath string, opts SelectOptions) (SelectResult, error) {
	if err := refuseOverlappingOutputs(exportPath, outPath, opts.RefusalsPath, opts.AttachmentsPath); err != nil {
		return SelectResult{}, err
	}
	in, err := os.Open(exportPath) //nolint:gosec // an operator-supplied export path is the whole point of the tool
	if err != nil {
		return SelectResult{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	defer func() { _ = in.Close() }()

	subset, err := atomicfile.Stage(outPath)
	if err != nil {
		return SelectResult{}, fmt.Errorf("-o: %w", err)
	}
	defer subset.Discard() // every Discard is a no-op once committed
	refusalFile, err := atomicfile.StageIf(opts.RefusalsPath)
	if err != nil {
		return SelectResult{}, fmt.Errorf("--refusals: %w", err)
	}
	defer refusalFile.Discard()
	attachmentFile, err := atomicfile.StageIf(opts.AttachmentsPath)
	if err != nil {
		return SelectResult{}, fmt.Errorf("--attachments: %w", err)
	}
	defer attachmentFile.Discard()

	var refusals *refusalLog
	if refusalFile != nil {
		refusals = &refusalLog{f: refusalFile}
	}
	res, rows, err := selectLibexRows(in, opts, refusals)
	if err != nil {
		return res, err
	}
	var buf bytes.Buffer
	for _, row := range rows {
		// Each row is its own bytes from the export, only insignificant whitespace
		// removed, so `metaimport libex` sees exactly the facts the dump stated - a
		// selection pass must never become a second mapping layer.
		buf.Reset()
		if err := json.Compact(&buf, row.raw); err != nil {
			return res, fmt.Errorf("write %s: %w", outPath, err)
		}
		buf.WriteByte('\n')
		subset.Write(buf.Bytes())
	}
	for _, a := range res.Attachments {
		attachmentFile.Encode(a)
	}
	res.UnnamedRefusals = refusals.flush(rows)
	// The subset LAST: its presence means the worklists are there with it.
	if err := atomicfile.CommitInOrder(refusalFile, attachmentFile, subset); err != nil {
		return res, err
	}
	return res, nil
}

// exclusions is a run's exclusion accounting: a refused row is counted under
// its rule and, when the worklist was asked for, written to it - one call, so
// the counts and the worklist cannot disagree.
type exclusions struct {
	res *SelectResult
	log *refusalLog
}

func (x exclusions) add(asin string, r refusal) {
	x.res.Excluded[r.report]++
	x.log.add(asin, r)
}

// hold is add for a duplicate-asin refusal whose first copy was KEPT at stream
// time: its line waits for the end, because it names an ASIN the subset may
// still carry (refusalLog.flush).
func (x exclusions) hold(asin string) {
	x.res.Excluded[reasonDuplicateASIN.report]++
	x.log.hold(asin)
}

// selectedRow is a kept row plus the facts the cap and the report need. raw is
// the row's own JSON bytes, never re-marshalled from the decoded map: the
// output of a selection pass must be the input row, not this package's
// rendering of it.
//
// Every kept completion carries a parseable series position (the completion
// rules refuse the rest), so pos is always meaningful and the cap needs no
// "unpositioned" tiebreak.
type selectedRow struct {
	raw        []byte
	seriesSlug string
	workKey    string
	pos        float64
	// book is the row as the import reads it (libexToBook), with the series
	// targets and cleaned author credits its last resolution left; title is its
	// work title slug, which a re-targeted row's workKey is rebuilt from.
	book  sourceBook
	title string
	// asin is the row's normalized ASIN, which a later exclusion names.
	asin string
	// attach is set for a row kept for ATTACHMENT (attach.go), nil for a
	// completion: an attachment claims no slot, is never cut by the cap and
	// counts in no per-series tally.
	attach *Attachment
}

// selectState is the within-export memory the per-row rules keep: the ASINs
// already seen, and the (series,
// position) slots already claimed by a selected row. All first-seen-wins, so a
// run is deterministic in input order.
type selectState struct {
	seenASIN map[string]bool
	// keptASIN is the ASINs a row was kept for at stream time: a later copy's
	// duplicate-asin line is held until the subset is known (refusalLog.flush).
	keptASIN map[string]bool
	// claimed maps "<series slug>\x00<position>" to the work key holding it.
	// The value matters because the per-region sibling rows of ONE title
	// legitimately claim the same slot - they are one work.
	claimed map[string]string
}

func newSelectState() *selectState {
	return &selectState{seenASIN: map[string]bool{}, keptASIN: map[string]bool{}, claimed: map[string]string{}}
}

// claimPosition reserves (series, position) for workKey, reporting false when
// an earlier row of this run claimed it for a different work (the two
// DE-sibling volumes libex lists at one position): the importer's addToSeries
// refuses the loser and creates the work ANYWAY, orphaned outside the series -
// exactly what a series COMPLETION must not produce. A position the CATALOGUE
// fills is decided before this (selectLibexRow).
func (st *selectState) claimPosition(slug, seq, workKey string) bool {
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
func selectLibexRows(r io.Reader, opts SelectOptions, refusals *refusalLog) (SelectResult, []selectedRow, error) {
	res := SelectResult{Excluded: map[string]int{}}
	x := exclusions{res: &res, log: refusals}
	idx, warnings := loadSeriesIndex(opts.DataDir, opts.AttachEditions)
	res.Warnings = append(res.Warnings, warnings...)

	st := newSelectState()
	var kept []selectedRow

	err := streamLibexRows(r, func(raw []byte, e rawBook) {
		res.RowsRead++
		row, asin, reason := selectLibexRow(e, idx, st, opts.AttachEditions)
		switch {
		case reason == reasonDuplicateASIN && st.keptASIN[asin]:
			x.hold(asin)
		case reason != refusal{}:
			x.add(asin, reason)
		default:
			row.raw = raw
			st.keptASIN[asin] = true
			kept = append(kept, row)
		}
	})
	if err != nil {
		return res, nil, err
	}

	// The cap and the batch re-check each change what the other sees: the cap can
	// cut the row that let another anchor, and a re-check can move or drop rows
	// the cap counted. So the two run until NEITHER drops anything, which leaves
	// exactly a set the import will resolve as confirmed and the cap no longer
	// cuts.
	cuts := map[string]SeriesCount{}
	for {
		before := len(kept)
		kept = confirmBatch(kept, idx, x, opts.AttachEditions)
		var cut map[string]SeriesCount
		kept, cut = applySeriesCap(kept, opts.MaxPerSeries, x)
		for slug, c := range cut {
			t := cuts[slug]
			t.Series = slug
			t.CutWorks += c.CutWorks
			t.CutRows += c.CutRows
			cuts[slug] = t
		}
		if len(kept) == before {
			break
		}
	}
	summarize(kept, cuts, idx, &res)
	return res, kept, nil
}

// confirmBatch re-resolves the kept rows TOGETHER, as the import of exactly
// this selection will (the importer resolves a batch from the catalogue plus a
// census of the batch's own rows, seriesresolve.go), under the batch's own
// credit decisions (creditContext, passed explicitly - the planner is shared).
// Each row keeps the first of its claims the batch sends to a catalogued series
// (completionClaim) - which need not be the one it matched alone: the batch can
// send it to another catalogued series of the same name, which is still a
// completion, and the row then claims its position there. A row the batch sends
// to no catalogued series is dropped under the stream-time reason, and one whose
// position in its new series is already taken under that reason - unless, with
// attachEditions, it is another edition of the work that held the position at
// load (attachFor, the rule the import asks too, under the same context), which
// keeps it for ATTACHMENT; the AUTHOR half of that rule is decided here and only
// here. Dropping a row can change the others' evidence, so the groups a dropped
// row claimed into - its resolution units (seriesUnits) - are resolved again until
// nothing more is dropped. A slot a
// dropped row held at STREAM time is not handed back: a sibling that lost the
// slot to it was excluded then, which only ever narrows a tranche.
func confirmBatch(kept []selectedRow, idx seriesIndex, x exclusions, attachEditions bool) []selectedRow {
	if len(kept) == 0 {
		return kept
	}
	cat := idx.catalogue()
	books := make([]sourceBook, len(kept))
	for i, r := range kept {
		books[i] = r.book
		books[i].authorCredits, books[i].creditsCached = nil, false
	}
	ctx := idx.p.creditContextOf(books)
	claims, where := idx.p.batchClaimsIn(ctx, books)
	// With attachEditions, the titles the import resolves each row's work by:
	// its edition-cleaned titles (on a copy of the decoded row - the next pass
	// must see the row the import sees) and the batch title pre-pass over them.
	// A plain selection never asks.
	var titles []string
	if attachEditions {
		for i := range books {
			books[i].raw = maps.Clone(books[i].raw)
		}
		normalizeEditionMarkers(books)
		titles = resolveWorkTitles(books)
	}

	owner := make([]int, len(claims))
	for ci, w := range where {
		owner[ci] = w.book
	}
	groups, keys := claimGroups(claims)
	units := seriesUnits(cat, claims, groups, keys)
	unitOf := make([]int, len(claims))
	for u, unit := range units {
		for _, k := range unit {
			for _, ci := range groups[k] {
				unitOf[ci] = u
			}
		}
	}
	alive := make([]bool, len(kept))
	for i := range alive {
		alive[i] = true
	}
	targets := make([]seriesTarget, len(claims))
	dirty := make([]int, len(units))
	for u := range dirty {
		dirty[u] = u
	}
	for len(dirty) > 0 {
		for _, u := range dirty {
			var members [][]int
			for _, k := range units[u] {
				var live []int
				for _, ci := range groups[k] {
					targets[ci] = seriesTarget{}
					if alive[owner[ci]] {
						live = append(live, ci)
					}
				}
				if len(live) > 0 {
					members = append(members, live)
				}
			}
			if len(members) > 0 {
				resolveSeriesUnit(cat, claims, members, targets, map[string]map[string]bool{})
			}
		}
		// Every claim's target is stamped on its row, as the import's
		// resolveSeriesTargets stamps it, so the verdict and the attach rule read
		// the batch's resolution off the row itself (completionClaim).
		for ci, t := range targets {
			books[where[ci].book].series[where[ci].ref].target = t
		}
		dropped := map[int]bool{}
		drop := func(i int, reason refusal) {
			alive[i] = false
			x.add(kept[i].asin, reason)
			for ci := range claims {
				if owner[ci] == i {
					dropped[unitOf[ci]] = true
				}
			}
		}
		claimed := map[string]string{}
		for i, r := range kept {
			if !alive[i] {
				continue
			}
			v := verdictOf(books[i])
			if !v.ok {
				drop(i, reasonSeriesAuthors)
				continue
			}
			if v.mints {
				drop(i, reasonOtherSeriesUncatalogued)
				continue
			}
			pos, posOK := seriesPositionValue(v.ref)
			if !posOK {
				drop(i, reasonNoPosition)
				continue
			}
			if occupant := idx.positions[v.slug][v.ref.seq]; occupant != "" {
				if !attachEditions {
					drop(i, reasonPositionTaken)
					continue
				}
				ws, _ := idx.p.attachFor(ctx, books[i], titles[i])
				if ws == nil {
					drop(i, reasonPositionTaken)
					continue
				}
				kept[i].seriesSlug, kept[i].workKey, kept[i].pos = v.slug, "", 0
				kept[i].attach = &Attachment{ASIN: r.asin, Work: ws.slug, Series: v.slug, Position: v.ref.seq}
				continue
			}
			workKey := v.slug + "\x00" + r.title
			key := v.slug + "\x00" + v.ref.seq
			if owned, taken := claimed[key]; taken && owned != workKey {
				drop(i, reasonPositionTaken)
				continue
			}
			claimed[key] = workKey
			kept[i].seriesSlug, kept[i].workKey, kept[i].pos, kept[i].attach = v.slug, workKey, pos, nil
		}
		dirty = dirty[:0:0]
		for u := range units {
			if dropped[u] {
				dirty = append(dirty, u)
			}
		}
	}
	out := kept[:0:0]
	for i, r := range kept {
		if alive[i] {
			out = append(out, r)
		}
	}
	return out
}

// selectLibexRow applies the per-row rules to one decoded row, returning the
// kept row, the row's ASIN (normalized when it is one, else as refusedRowASIN
// names it) and the first rule it failed (the zero refusal when kept). st
// carries the within-export dedup set and position claims, and is updated as
// the rules pass.
//
// The rules are ordered so the counts read usefully: identity first (a row we
// cannot address, or already have), then the completion test that defines the
// tranche, and only then the mapping and credit tests - so "unmapped language"
// counts rows we actually wanted, not the 99 percent of the dump that was never
// in scope. The position claim comes last because it MUTATES state: only a row
// that would otherwise be kept may reserve a slot.
func selectLibexRow(e rawBook, idx seriesIndex, st *selectState, attachEditions bool) (selectedRow, string, refusal) {
	asin := NormalizeASIN(e.str("asin"))
	if asin == "" {
		return selectedRow{}, refusedRowASIN(e.str("asin")), reasonNoASIN
	}
	if idx.asins[asin] {
		return selectedRow{}, asin, reasonAlreadyASIN
	}
	if st.seenASIN[asin] {
		return selectedRow{}, asin, reasonDuplicateASIN
	}
	st.seenASIN[asin] = true

	// The series a row completes is one its authors may JOIN (seriesauthors.go):
	// a same-named series of another author's is not a completion - the importer
	// would mint a new series for the row - so it is reported as such.
	// A row naming no series the catalogue's chains could hold is out before its
	// book is composed - the whole dump streams through here.
	refs := libexSeries(e["series"])
	if !idx.namesACatalogueChain(refs) {
		return selectedRow{}, asin, reasonNoSeries
	}
	// So is one in another language (languageCloses): the German "Orphan X" is
	// not completed by an English volume of it.
	v, book := idx.match(idx.libexBook(e, asin))
	if !v.ok {
		switch {
		case v.otherLanguage:
			return selectedRow{}, asin, reasonSeriesLanguage
		case v.othersOnly:
			return selectedRow{}, asin, reasonSeriesAuthors
		}
		return selectedRow{}, asin, reasonNoSeries
	}
	// A completion may not ALSO be a series creation, and that is a rule about
	// EVERY claim the importer acts on, not just the one that matched here
	// (mintsSeries says which claims those are): a row that completes one
	// catalogued series and mints another is exactly what this tool exists to make
	// impossible (never a mirror, never a new series).
	if v.mints {
		return selectedRow{}, asin, reasonOtherSeriesUncatalogued
	}
	slug, ref := v.slug, v.ref
	// A row that names a series but no usable position in it is not a
	// completion: the importer would create the work and then warn that it
	// could not be placed, leaving an orphan work outside the series it was
	// selected to complete.
	pos, posOK := seriesPositionValue(ref)
	if !posOK {
		return selectedRow{}, asin, reasonNoPosition
	}
	if _, langOK := mapLanguage(e.str("language")); !langOK {
		return selectedRow{}, asin, reasonLanguage
	}
	if _, _, regionOK := libexRegion(e); !regionOK {
		return selectedRow{}, asin, reasonRegion
	}
	// The credit-side refusals the parse layer applies (refuseLibexCredits). A
	// row the importer will refuse must not be selected: it would be counted as a
	// completion the tranche does not actually deliver, and - because the position
	// claim below is first-seen-wins - it would take the slot away from a sibling
	// row that IS importable.
	if r, refused := refuseLibexCredits(libexNames(e["authors"]), libexNames(e["narrators"])); refused {
		return selectedRow{}, asin, r.reason
	}

	// The work key is the importer's own work identity as far as a selection
	// pass can know it: the slug of the work title (libex's "title", which
	// libexToBook carries as title_short) within the matched series. Two
	// per-region sibling rows of one title therefore share a key and project
	// ONE new work, which is what the importer's same-narrator ASIN merge
	// actually does with them.
	title := Slugify(book.str("title_short"))
	// A position the CATALOGUE already fills is not a completion. With
	// attachEditions a row there that passes the census-independent half of the
	// attach rule (attachCandidate) is kept as a CANDIDATE - its authors are
	// judged in confirmBatch, under the batch's credit decisions - and reserves
	// no slot, since the slot is the incumbent's.
	if occupant := idx.positions[slug][ref.seq]; occupant != "" {
		if !attachEditions {
			return selectedRow{}, asin, reasonPositionTaken
		}
		ws := idx.p.attachCandidate(book, idx.names[slug], occupant, ref)
		if ws == nil {
			return selectedRow{}, asin, reasonPositionTaken
		}
		return selectedRow{
			seriesSlug: slug, book: book, title: title, asin: asin,
			attach: &Attachment{ASIN: asin, Work: ws.slug, Series: slug, Position: ref.seq},
		}, asin, refusal{}
	}
	workKey := slug + "\x00" + title
	if !st.claimPosition(slug, ref.seq, workKey) {
		return selectedRow{}, asin, reasonPositionTaken
	}
	return selectedRow{seriesSlug: slug, workKey: workKey, pos: pos, book: book, title: title, asin: asin}, asin, refusal{}
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
//
// A row kept for ATTACHMENT is outside the cap: it adds no work, so it is
// neither counted against a series nor cut.
func applySeriesCap(rows []selectedRow, maxPerSeries int, x exclusions) ([]selectedRow, map[string]SeriesCount) {
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
		if row.attach != nil {
			continue
		}
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
		if row.attach == nil && cut[row.workKey] {
			x.add(row.asin, reasonSeriesCap)
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
		if row.attach != nil {
			res.Attachments = append(res.Attachments, *row.attach)
			continue
		}
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
	// The cap cuts a series down to maxPerSeries (>= 1) works, but the cap and
	// the batch re-check run to a fixpoint, and a later re-check can drop the
	// works the cap kept - so a cut series need not have a tally. Such a series
	// is reported on its own (CutOnly), never dereferenced as though it did.
	for slug, cut := range cuts {
		t := tallies[slug]
		if t == nil {
			res.CutOnly = append(res.CutOnly, SeriesCount{Series: slug, Name: idx.names[slug], CutWorks: cut.CutWorks, CutRows: cut.CutRows})
			continue
		}
		t.CutWorks, t.CutRows = cut.CutWorks, cut.CutRows
	}
	sort.Slice(res.CutOnly, func(i, j int) bool { return res.CutOnly[i].Series < res.CutOnly[j].Series })
	res.ProjectedWorks = len(works)
	// A tally exists only because a completion was kept for it, and a kept
	// completion always contributes a work, so every listed series is a matched
	// one.
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
// can complete, the ASINs already recorded, and the importer's own planner over
// the same catalogue, through which every series claim is resolved - so a
// selection and the import of what it selects judge a row by one rule, one
// person resolution and one work identity.
type seriesIndex struct {
	bySlug map[string]string // slug -> series name
	names  map[string]string // same map, read under its reporting name
	asins  map[string]bool
	// positions maps a series slug to the positions its works occupied when the
	// catalogue was loaded (canonical position -> work id). With the attach rule
	// it is seriesState.loaded, the very map the import's attach rule reads (every
	// listed position); a plain selection keeps its long-standing one position per
	// work.
	positions map[string]map[string]string
	// redirects is the catalogue's tombstone table (tombstone.go).
	redirects model.Redirects
	// p is a planner loaded over the catalogue that plans nothing: the series
	// resolution, the credit cleaning, the person resolution and the work
	// identity (resolveWork, check.WorkIdentity) are all its own. It is never
	// given a batch's credit decisions: those are passed explicitly
	// (creditContext).
	p *planner
}

// loadSeriesIndex reads the catalogue at dataDir through an importer planner. A
// tree with validation problems is still used (best-effort, exactly like the
// importer's loadExisting) but is warned about: selecting against a half-loaded
// catalogue would silently re-import books that are already there.
// PROFILE: bare dataDir = ProfileAll by Options.Profile's own default rule
// (types.go carries the full statement; adding a --profile flag to this CLI
// means threading it here too).
//
// attach says the run applies the attach rule; without it the planner lets go
// of the normalized-identity index, exactly as a plain selection always has.
func loadSeriesIndex(dataDir string, attach bool) (seriesIndex, []string) {
	idx := seriesIndex{
		bySlug:    map[string]string{},
		asins:     map[string]bool{},
		positions: map[string]map[string]string{},
	}
	idx.names = idx.bySlug
	store, err := openStore(dataDir, pack.ProfileAll)
	if err != nil {
		return idx, []string{fmt.Sprintf("catalogue at %s: %v; selecting against nothing", dataDir, err)}
	}
	p := newPlanner(store, sourceLibex, Options{DataDir: dataDir})
	p.loadedPositions = attach
	p.loadExisting()
	if !attach {
		p.identity = nil // only the attach rule asks it (attach.go)
	}
	p.seriesAuthorIndex()
	p.catalog = nil
	idx.p, idx.asins, idx.redirects = p, p.asins, p.redirects
	var warnings []string
	if p.loadProblems > 0 {
		warnings = append(warnings, fmt.Sprintf("catalogue at %s has %d validation problem(s); selecting against it best-effort", dataDir, p.loadProblems))
	}
	for slug, ss := range p.series {
		idx.bySlug[slug] = ss.name
		if attach {
			// Every position the series lists, as the attach rule reads it
			// (seriesState.loaded).
			idx.positions[slug] = ss.loaded
			continue
		}
		// A plain selection's long-standing reading: one position per work (the
		// series' members map), compared in the canonical spelling a row's claim
		// arrives in, so a stored "1.0" and a claimed "1" are one slot.
		taken := make(map[string]string, len(ss.members))
		for work, pos := range ss.members {
			if norm, ok := NormalizeSequence(pos); ok {
				pos = norm
			}
			taken[pos] = work
		}
		idx.positions[slug] = taken
	}
	return idx, warnings
}

// libexBook is a libex row as the import reads it: the sourceBook the parse
// layer builds (libexToBook, with its text decoded as runBooks decodes it and
// the row's marketplace), which is what the planner resolves its series claims from.
func (idx seriesIndex) libexBook(e rawBook, asin string) sourceBook {
	region, _, _ := libexRegion(e)
	book := libexToBook(e, asin, region, libexNames(e["authors"]), libexNames(e["narrators"]), &libexParse{})
	// This door composes a book runBooks never sees, so it decodes the book's
	// text itself - exactly once, as runBooks does for the import (entities.go).
	book.decodeText()
	return book
}

// seriesVerdict is where ALL of a row's series claims landed. A selection reads
// two things off it: the completion it was selected for (completionClaim), and
// whether any OTHER claim the importer acts on would mint a series - which a
// completion may not do.
type seriesVerdict struct {
	// slug and ref are the completion claim's series and ref (the caller reads
	// its position); ok says there was one.
	slug string
	ref  seriesRef
	ok   bool
	// othersOnly reports, when nothing matched, that some claim named a
	// catalogued series belonging to other authors or in another language, and
	// otherLanguage that at least one of those was closed by its language - the
	// more specific reason, so it is the one reported.
	othersOnly, otherLanguage bool
	// mints reports that some claim the importer would ACT on resolves to no
	// catalogued series, so importing the row would create one.
	mints bool
}

// match resolves a row's series claims against the catalogue exactly as the
// importer's batch pre-pass would for this row alone (seriesresolve.go), stamps
// every claim's target on the row, and returns the verdict plus the row as that
// left it - its targets and its cleaned author credits, so nothing downstream
// parses them again.
func (idx seriesIndex) match(book sourceBook) (seriesVerdict, sourceBook) {
	books := []sourceBook{book}
	claims, where := idx.p.batchClaims(books)
	for k, t := range resolveSeriesClaims(idx.catalogue(), claims) {
		books[0].series[where[k].ref].target = t
	}
	return verdictOf(books[0]), books[0]
}

// verdictOf reads a row's verdict off its resolved targets: the completion is
// completionClaim (the first claim that landed in a catalogued series - the one
// the import's attach rule reads too), any claim the importer acts on that
// landed in no catalogued series would mint one, and a same-named series a claim
// did not fit is what othersOnly reports when nothing landed. The batch re-check
// reads the batch's own targets through this too (confirmBatch), so the two
// passes cannot read one row's claims differently.
func verdictOf(b sourceBook) seriesVerdict {
	var v seriesVerdict
	if r, at := completionClaim(b); at >= 0 {
		v.slug, v.ref, v.ok = r.target.slug, r, true
	}
	for _, r := range b.series {
		if mintsSeries(r.target, r) {
			v.mints = true
		}
		if !v.ok && len(r.target.stepped) > 0 {
			v.othersOnly = true
			for _, s := range r.target.stepped {
				if s.language != "" {
					v.otherLanguage = true
				}
			}
		}
	}
	return v
}

// mintsSeries reports whether importing a row would CREATE a series for this
// claim. The create path acts on every claim with a usable position (addBook's
// loop over b.series) and getOrCreateSeries creates the series its target names
// whenever the catalogue does not already hold it - so a target that is not
// found, but has an addressable slug, is a new series. The two claims the
// importer only warns about mint nothing and so disqualify nothing: one with no
// usable position (`!seqOK`) is never placed, and one whose name has no
// addressable slug at all (an empty target slug) has no identity to mint.
//
// The `!seqOK` half needs the series-position lookup never to fill a claim this
// predicate cleared - fillSeriesPositions rewrites a missing position from the
// live service BEFORE the series loop reads it, which would turn such a claim
// into a placed, and so minting, one. Nothing about the operator's command line
// decides that: the lookup is installed only for a USER-LIBRARY create run
// (runBooks' `p.userTier` gate) and `libex-import` is bulk-mirror tier
// (pkg/model/trust.go), so `metaimport libex --series-lookup` is a no-op over a
// tranche. Opening the lookup to the bulk mirror is therefore a change to make
// here too.
func mintsSeries(t seriesTarget, ref seriesRef) bool {
	return ref.seqOK && !t.found && t.slug != ""
}

// namesACatalogueChain reports whether any claim's name has a catalogue series
// (or a retired one) at the first slug of its chain, or names the BASE of one the
// qualifier index holds (seriesqualified.go) - the cheap necessary condition for
// resolving into a series the tree holds, since a chain's first free slug ends
// it and the index is the only other way in.
func (idx seriesIndex) namesACatalogueChain(refs []seriesRef) bool {
	for _, r := range refs {
		base := Slugify(r.name)
		if base == "" {
			continue
		}
		first := SeriesSlugAt(base, 0)
		if _, held := idx.bySlug[first]; held {
			return true
		}
		if _, retired := idx.redirects.Survivor(model.RedirectSeries, first); retired {
			return true
		}
		if idx.holdsQualifiedBase(r.name, base) {
			return true
		}
	}
	return false
}

// holdsQualifiedBase is SeriesAuthorIndex.holdsQualifiedBase over the planner's
// index; base is the name's own slug.
func (idx seriesIndex) holdsQualifiedBase(name, base string) bool {
	return idx.p != nil && idx.p.seriesIndex.holdsQualifiedBase(name, base)
}

// catalogue is the index as the series resolution reads it: the planner's own.
func (idx seriesIndex) catalogue() seriesCatalogue {
	if idx.p == nil {
		return seriesCatalogue{stored: func(string) (string, bool) { return "", false }}
	}
	return idx.p.seriesCatalogue()
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
	// The total counts the COMPLETION rows; a row kept for attachment adds no
	// work and is reported on its own line, printed only when there is one, so a
	// selection that attached nothing reads exactly as it always has.
	completions := r.RowsSelected - len(r.Attachments)
	fmt.Fprintf(&b, "  total: %d series, %d %s, %d %s\n",
		len(r.PerSeries), r.ProjectedWorks, plural(r.ProjectedWorks, "work"),
		completions, plural(completions, "row"))
	if n := len(r.Attachments); n > 0 {
		works := map[string]bool{}
		for _, a := range r.Attachments {
			works[a.Work] = true
		}
		fmt.Fprintf(&b, "  attached: %d %s to %d catalogued %s already at the position claimed (another recording, or another ASIN on one)\n",
			n, plural(n, "row"), len(works), plural(len(works), "work"))
	}

	fmt.Fprintf(&b, "excluded %d rows:\n", r.RowsRead-r.RowsSelected)
	for _, rule := range refusals {
		fmt.Fprintf(&b, "  %-38s %d\n", rule.report, r.Excluded[rule.report])
	}
	// Printed only when a --refusals worklist was asked for and could not name a
	// row, so every other report reads exactly as it did.
	if r.UnnamedRefusals > 0 {
		fmt.Fprintf(&b, "  (the --refusals worklist omits %d refused %s that state no ASIN)\n",
			r.UnnamedRefusals, plural(r.UnnamedRefusals, "row"))
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

// cappedSeries lists every series the cap cut from, in report order, then the
// ones it cut from that kept no completion at all.
func (r SelectResult) cappedSeries() []SeriesCount {
	var out []SeriesCount
	for _, s := range r.PerSeries {
		if s.CutWorks > 0 {
			out = append(out, s)
		}
	}
	return append(out, r.CutOnly...)
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
