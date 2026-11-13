package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// copies.go is the DIRECT-ATTACHMENT half of the recordings-only pass
// (`metaimport libex --recordings-only --copies-of <file>`): a row the caller
// has already PROVEN to be another marketplace's copy of a catalogued
// production is merged as one more ASIN onto the recording its anchor ASIN
// sits on.
//
// The proof is the caller's, not the importer's. audiosilo-meta-sync, the
// series-completion bot, lists a row only when its libex skuGroup equals the
// anchor's (one production sold in several marketplaces). The importer knows
// nothing of skuGroups, so it never re-derives the claim; what it does is take
// the claim at its word for IDENTITY and keep every PHYSICAL guard a merge
// has:
//
//   - the anchor ASIN must be catalogued (copy-anchor-unknown),
//   - the row's language must be the recording's (copy-language),
//   - the runtimes must agree (copy-runtime-mismatch: sameRuntime, the
//     same-production test every merge uses),
//   - an abridged copy never joins an unabridged recording or the other way
//     round (copy-abridged: abridgedConflict, asked of the RECORD's own flag,
//     since a recording loaded from disk carries none on its recInfo),
//   - the region must map (unmapped-region) and the recording must not already
//     carry ANOTHER ASIN in it (copy-region-held: one production has one
//     listing per marketplace, so a second is another product).
//
// What the pass never reads is the row's CREDITS or SERIES: a listed row is
// kept out of the batch's credit decisions and series resolution (run), resolves no
// work by title and authors, and so can mint no person, fork no work and touch
// no series, whatever its credits say. That is the point: the six regional
// copies of work reborn-as-a-demonic-tree-8 credit ["XKarnation","XKarnation
// XKarnation"], which the identity machinery read as another author's book.
//
// A row the file does not list goes through the ordinary recordings-only path
// in the same run.
//
// One door stands ahead of the pass: the libex parse layer (parseRow). A
// listed copy with a malformed ASIN or an unmapped region is refused there
// under that rule's own code, as is one crediting an AI voice (ai-narrator:
// an AI-narrated listing is another production). Its other credit refusals -
// a platform account, a cast placeholder, a semicolon-joined list, a name that
// slugs to nothing (kana, kanji) - are skipped for a listed row
// (refuseLibexCopyCredits), since this pass reads no credit and so cannot
// mint the person those rules guard against. Unlisted rows keep them all.

// copyOf is one line of the --copies-of file: the copy row's ASIN and the
// catalogued ASIN whose recording it is another marketplace's listing of.
// The rows themselves arrive through the run's usual libex rows input.
type copyOf struct {
	ASIN   string `json:"asin"`
	Anchor string `json:"anchor"`
}

// ReadCopiesOf reads a --copies-of file: one {"asin","anchor"} JSON object per
// line, blank lines ignored, unknown keys ignored (the writer may say more
// than this reader needs). Both ASINs must be well-formed and differ, and a
// copy may name one anchor only. It returns copy ASIN -> anchor ASIN,
// normalized.
func ReadCopiesOf(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseCopiesOf(f)
}

func parseCopiesOf(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c copyOf
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		asin, anchor := NormalizeASIN(c.ASIN), NormalizeASIN(c.Anchor)
		switch {
		case asin == "" || anchor == "":
			return nil, fmt.Errorf("line %d: asin %q and anchor %q must both be well-formed ASINs", n, c.ASIN, c.Anchor)
		case asin == anchor:
			return nil, fmt.Errorf("line %d: %s is listed as a copy of itself", n, asin)
		}
		if prev, listed := out[asin]; listed && prev != anchor {
			return nil, fmt.Errorf("line %d: %s is listed as a copy of both %s and %s", n, asin, prev, anchor)
		}
		out[asin] = anchor
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// splitCopies separates the rows a --copies-of run attaches directly (listed
// by ASIN in p.copiesOf) from the rest, keeping each side in input order.
// Without the option every row is a plain row.
func (p *planner) splitCopies(books []sourceBook) (rows, copies []sourceBook) {
	if len(p.copiesOf) == 0 {
		return books, nil
	}
	for _, b := range books {
		if _, listed := p.copiesOf[NormalizeASIN(b.str("asin"))]; listed {
			copies = append(copies, b)
			continue
		}
		rows = append(rows, b)
	}
	return rows, copies
}

// planCopies attaches each listed copy row to its anchor's recording.
func (p *planner) planCopies(books []sourceBook) {
	normalizeEditionMarkers(books)
	for _, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		p.setSource(asin, b)
		p.attachCopy(b, asin, p.copiesOf[asin])
		if p.fatal != nil {
			return
		}
	}
}

// attachCopy merges row b's ASIN onto the recording anchor sits on, or refuses
// it under one of the copy codes (refusalcodes.go). An ASIN the catalogue
// already holds is the ordinary dedup skip.
func (p *planner) attachCopy(b sourceBook, asin, anchor string) {
	if p.dedupeByASIN(asin) {
		return
	}
	warn := p.bookWarn(b)
	ref, located := p.asinLoc[anchor]
	var ri *recInfo
	if ws := p.works[ref.Work]; located && ws != nil {
		ri = ws.recs[ref.Rec]
	}
	if ri == nil {
		warn("copy of %s: the anchor ASIN is not in the catalogue; not attached", anchor)
		p.noteSkip(b, reasonCopyAnchorUnknown)
		return
	}
	if ri.librivox {
		// A LibriVox project is a production of its own (addRecording's rule):
		// it takes no retail ASIN, however the anchor came to sit on it.
		warn("copy of %s: the anchor sits on LibriVox project %s, which takes no retail copy; not attached",
			anchor, recLabel(ref.Work, ref.Rec))
		p.noteSkip(b, reasonCopyAnchorUnknown)
		return
	}
	lang, ok := p.admitLanguage(b, warn)
	if !ok {
		return
	}
	_, raw := p.recordingRaw(ref.Work, ref.Rec)
	if raw == nil {
		return
	}
	recLang := coerceStr(raw["language"])
	if recLang == "" {
		recLang = p.works[ref.Work].lang
	}
	if !langCompatible(recLang, lang) {
		warn("copy of %s: language %s, but %s is %s; not attached", anchor, lang, recLabel(ref.Work, ref.Rec), recLang)
		p.noteSkip(b, reasonCopyLanguage)
		return
	}
	// The record's own flag, not ri.abridged: loadExisting leaves that nil for
	// a recording read from disk (recInfo.abridged), and every anchor is one,
	// so asking it would refuse an abridged copy of an abridged recording and
	// let an unabridged copy join it.
	if abridgedConflict(coerceBoolPtr(raw["abridged"]), b.abridged) {
		warn("copy of %s: abridged and unabridged editions are two productions; not attached", anchor)
		p.noteSkip(b, reasonCopyAbridged)
		return
	}
	if !sameRuntime(ri.known, b.evidence) {
		warn("copy of %s: runtime %d min does not match %s; not attached", anchor, b.runtimeMin, recLabel(ref.Work, ref.Rec))
		p.noteSkip(b, reasonCopyRuntime)
		return
	}
	region, ok := p.resolveASINRegion(b, warn)
	if !ok {
		p.noteSkip(b, reasonRegion)
		return
	}
	if held := asinInRegion(raw, region); held != "" {
		warn("copy of %s: %s already carries %s in region %s; not attached", anchor, recLabel(ref.Work, ref.Rec), held, region)
		p.noteSkip(b, reasonCopyRegionHeld)
		return
	}
	if !p.mergeRecordingASIN(ri, b, ref, region, asin, warn) {
		return
	}
	p.registerASIN(asin, ref)
	p.summary.CopiesAttached++
}

// asinInRegion is the ASIN a recording's raw asin[] carries in region, "" when
// it carries none there.
func asinInRegion(raw map[string]any, region string) string {
	arr, _ := raw["asin"].([]any)
	for _, v := range arr {
		m, _ := v.(map[string]any)
		if coerceStr(m["region"]) == region {
			return coerceStr(m["asin"])
		}
	}
	return ""
}
