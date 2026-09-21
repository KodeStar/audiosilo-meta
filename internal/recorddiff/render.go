package recorddiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// render.go turns a computed Diff into the text a reviewer (or a model) reads.
//
// THE BUDGET IS THE POINT. The consumer this was written for feeds the result to
// a language model with a hard input cap, and the failure it replaces was a
// SILENT one: the old raw diff was cut with head -c, so the reviewer saw the
// first tenth of a tranche and had no way to know a ninth of it was missing. So
// the render fits the budget by dropping WHOLE entries and says how many it
// dropped, never by cutting a line in half, and the header and the per-family
// counts are never dropped at all - the counts are how a reader knows the
// tranche was bigger than what follows.
//
// Sections are ALLOCATED their budget in a different order from the one they are
// PRINTED in. Added entries are by far the bulk, so they are served last and take
// whatever is left; the small sections (removed records, tombstones, modified
// records) are served first and can never be starved by a tranche of a thousand
// additions. Each of those is still capped at half of what remains when it is
// served, so no one of them can starve the rest either.
//
// EVERY LINE IS ONE LINE. A rendered line carries free text a contributor wrote
// - a title, a person's name, a pack's parse error - and the schema caps that
// text's length and nothing else, so it may hold a newline. One record could
// otherwise open a second line, and a summary whose shape the data controls is a
// summary that can forge a count, a warning, or an omission line saying nothing
// was dropped. So every line this file emits goes through oneLine, and the two
// functions below are the only places a line is built.

// DefaultMaxBytes is the render budget the command defaults to. It sits under the
// ai-verify wrapper's own MAX_INPUT_BYTES, which stays as the outer bound: this
// one decides WHAT is dropped, that one is the last resort that decides nothing.
const DefaultMaxBytes = 180000

// indent is what a modified record's field lines are indented by.
const indent = "    "

// block is one entry's rendered lines, kept together: a block is either wholly
// printed or wholly omitted.
type block struct {
	family pack.Family
	lines  []string
}

// size is the byte cost of printing the block.
func (b block) size() int {
	n := 0
	for _, l := range b.lines {
		n += len(l) + 1
	}
	return n
}

// section is a titled run of blocks, plus the noun its omission line counts in.
type section struct {
	title  string
	noun   string
	blocks []block
}

// titleSize is the byte cost of the section's title line plus the blank line
// before it. It is one function because writeSection charges it against the
// budget it spends and section.size() charges it against the budget it asks for;
// two spellings of it would drift.
func (s section) titleSize() int { return len(s.title) + 2 }

// size is the byte cost of printing the section whole.
func (s section) size() int {
	if len(s.blocks) == 0 {
		return 0
	}
	n := s.titleSize()
	for _, b := range s.blocks {
		n += b.size()
	}
	return n
}

// Text renders the diff, dropping whole entries as needed to fit maxBytes. A
// maxBytes of zero or less renders everything.
func (d *Diff) Text(maxBytes int) string {
	head := d.headLines()
	secs := d.sections()

	var b strings.Builder
	for _, l := range head {
		b.WriteString(l)
		b.WriteString("\n")
	}

	budgets := allocate(secs, maxBytes, b.Len())
	for i, s := range secs {
		writeSection(&b, s, budgets[i])
	}
	return b.String()
}

// JSON renders the diff as a machine-readable object. It is the shape tests and
// other tools read; nothing truncates it.
func (d *Diff) JSON() ([]byte, error) {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// oneLine flattens anything that would break a rendered line into a single
// space: the ASCII control characters (a newline above all), and the Unicode
// line and paragraph separators a JSON string may also carry. It is what makes
// "one record, one line" a property of the renderer rather than a hope about the
// data - see the file header.
//
// A cheaper-looking alternative, quoting every value, is not equivalent: a field
// diff's own path segments and a warning's embedded parse error are assembled
// text, not one quotable value.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '\u0085' || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}

// headLines are the lines that are never dropped: what this render is, the range
// it covers, the per-family counts, and anything the comparison could not read.
func (d *Diff) headLines() []string {
	out := []string{
		"AudioSilo Meta - ENTRY-LEVEL SUMMARY of a data change (not a text diff).",
	}
	if d.Base != "" || d.Head != "" {
		out = append(out, fmt.Sprintf("range: %s..%s", d.Base, d.Head))
	}
	out = append(out, fmt.Sprintf("changed files under the data root: %d", d.Files))
	out = append(out, "")

	fams := make([]pack.Family, 0, len(d.Counts))
	for f := range d.Counts {
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool { return familyLess(fams[i], fams[j]) })
	if len(fams) == 0 {
		out = append(out, "no pack entries changed")
	}
	for _, f := range fams {
		c := d.Counts[f]
		out = append(out, fmt.Sprintf("%s: %d added, %d removed, %d modified, %d moved-only",
			f.Root(), c.Added, c.Removed, c.Modified, c.Moved))
	}
	out = append(out, fmt.Sprintf("redirects: %d added, %d removed", len(d.Redirects.Added), len(d.Redirects.Removed)))

	if d.Empty() {
		// The whole point of the tool, stated outright for the one tranche where
		// the counts alone would be read as "nothing here to look at, probably
		// truncated": every difference between the two revisions is storage.
		out = append(out, "",
			"NO RECORD-LEVEL CHANGE: every difference in this tranche is storage churn",
			"(a pack re-render, split or rebind). No record was added, removed or edited.")
	}

	if len(d.Warnings) > 0 {
		out = append(out, "")
		for _, w := range d.Warnings {
			out = append(out, "! "+oneLine(w))
		}
	}
	return out
}

// sections builds the printable body, in PRINT order.
func (d *Diff) sections() []section {
	added := section{title: fmt.Sprintf("ADDED (%d)", len(d.Added)), noun: "added"}
	for _, e := range d.Added {
		added.blocks = append(added.blocks, block{family: e.Family, lines: []string{"+ " + oneLine(e.Summary)}})
	}

	modified := section{title: fmt.Sprintf("MODIFIED (%d)", len(d.Modified)), noun: "modified"}
	for _, c := range d.Modified {
		lines := []string{oneLine(fmt.Sprintf("~ %s %s", familyWord(c.Family), c.Slug))}
		for _, f := range c.Fields {
			lines = append(lines, indent+oneLine(f))
		}
		modified.blocks = append(modified.blocks, block{family: c.Family, lines: lines})
	}

	removed := section{title: fmt.Sprintf("REMOVED (%d)", len(d.Removed)), noun: "removed"}
	for _, e := range d.Removed {
		removed.blocks = append(removed.blocks, block{family: e.Family, lines: []string{"- " + oneLine(e.Summary)}})
	}

	redirects := section{title: fmt.Sprintf("REDIRECTS (%d)", d.Redirects.Len()), noun: "redirect"}
	for _, p := range d.Redirects.Added {
		redirects.blocks = append(redirects.blocks, block{lines: []string{oneLine(fmt.Sprintf("+ %s %s -> %s", p.Kind, p.Old, p.New))}})
	}
	for _, p := range d.Redirects.Removed {
		redirects.blocks = append(redirects.blocks, block{lines: []string{oneLine(fmt.Sprintf("- %s %s -> %s", p.Kind, p.Old, p.New))}})
	}

	return []section{added, modified, removed, redirects}
}

// allocate divides what is left of maxBytes, after the head lines already cost
// headUsed, between the sections.
//
// A maxBytes of zero or less means no limit and every section is given -1. With
// a limit, sections are served in REVERSE print order - so the bulk section
// (added entries, printed first) is served last and takes what is left, and the
// small ones cannot be starved by a tranche of a thousand additions; each of the
// others is capped at half of what remains when it is served, so no one of them
// can starve the rest either. A budget the head lines have already overrun is
// zero rather than unlimited, so a tiny maxBytes yields the head lines alone.
func allocate(secs []section, maxBytes, headUsed int) []int {
	out := make([]int, len(secs))
	if maxBytes <= 0 {
		for i := range out {
			out[i] = -1
		}
		return out
	}
	remaining := max(maxBytes-headUsed, 0)
	for i := len(secs) - 1; i >= 0; i-- {
		share := remaining
		if i > 0 {
			share = remaining / 2
		}
		share = min(share, secs[i].size())
		out[i] = share
		remaining -= share
	}
	return out
}

// writeSection prints as many whole blocks as budget allows, then says how many
// it left out. A budget of -1 means no limit.
//
// The title always prints, even when nothing fits under it, because the title
// carries the section's TOTAL count: it is a header in the sense the file's
// contract uses - a reader must be able to tell that entries were omitted.
func writeSection(b *strings.Builder, s section, budget int) {
	if len(s.blocks) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(s.title)
	b.WriteString("\n")

	// A section that fits WHOLE needs no omission line, so it must not be made to
	// reserve room for one. allocate caps a section's budget at its own size, so
	// "the budget is exactly what printing this costs" is the ORDINARY case, not
	// an edge - and demanding omissionReserve on top of it dropped the tail of
	// every small section, or the whole of one whose blocks are short (a tranche
	// of one work and two people rendered as nothing but an omission line saying
	// the tranche was over budget, which was false). The reserve belongs to the
	// truncating path alone.
	whole := budget < 0 || s.size() <= budget

	used := s.titleSize()
	shown := 0
	for i, blk := range s.blocks {
		if !whole {
			// Room for this block, and - unless it is the last one - for the
			// omission line that would follow it, so the last thing printed is
			// never an unannounced cut. The reserve is NOT demanded of the final
			// block: a section whose remaining blocks all fit would otherwise
			// drop the last of them to hold room for a line it will not print.
			need := used + blk.size()
			if i < len(s.blocks)-1 {
				need += omissionReserve
			}
			if need > budget {
				break
			}
		}
		for _, l := range blk.lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
		used += blk.size()
		shown++
	}
	if shown < len(s.blocks) {
		b.WriteString(omissionLine(s, s.blocks[shown:]))
		b.WriteString("\n")
	}
}

// omissionReserve is the room writeSection keeps back for an omission line. It is
// a fixed, generous allowance rather than the exact length, because the exact
// length is not known until it is decided how many blocks were dropped.
const omissionReserve = 200

// omissionLine names what was left out: by family when the dropped blocks are
// all one family, and with a breakdown when they are not.
//
// It is the ONE thing that makes a budgeted render honest, so it always states
// the count and always says why, and a reader who sees it knows the tranche was
// larger than the summary.
const omissionTail = " (the tranche is larger than this summary's budget)"

func omissionLine(s section, dropped []block) string {
	byFamily := map[pack.Family]int{}
	for _, blk := range dropped {
		byFamily[blk.family]++
	}
	fams := make([]pack.Family, 0, len(byFamily))
	for f := range byFamily {
		if f != "" {
			fams = append(fams, f)
		}
	}
	sort.Slice(fams, func(i, j int) bool { return familyLess(fams[i], fams[j]) })

	if len(fams) == 1 && len(byFamily) == 1 {
		return fmt.Sprintf("... %d more %s %s omitted%s",
			len(dropped), s.noun, countedFamily(byFamily[fams[0]], fams[0]), omissionTail)
	}
	line := fmt.Sprintf("... %d more %s entries omitted", len(dropped), s.noun)
	if len(fams) > 0 {
		parts := make([]string, 0, len(fams))
		for _, f := range fams {
			parts = append(parts, fmt.Sprintf("%d %s", byFamily[f], countedFamily(byFamily[f], f)))
		}
		line += " - " + strings.Join(parts, ", ")
	}
	return line + omissionTail
}
