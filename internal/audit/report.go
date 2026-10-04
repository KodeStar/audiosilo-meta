package audit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/reportdir"
)

// opPhrase is what each op DOES, in words, as a sentence opener. The prose a report
// shows is composed from it plus the proposal's own fields, so a detector states its
// intent once (typed) and the wording exists once (here) - rather than every
// detector carrying a hand-written sentence that could describe something other than
// what it proposed.
var opPhrase = map[string]func(Proposal) string{
	OpMergeWorks: func(p Proposal) string {
		return "review as one work: fold " + truncateList(p.Others, 8) + " onto " + p.Target
	},
	OpMergeSeries: func(p Proposal) string {
		return "review as one series: fold " + truncateList(p.Others, 8) + " onto " + p.Target
	},
	OpRetitle: func(p Proposal) string {
		return fmt.Sprintf("retitle %s: %s -> %s", p.Target, quoteOrDash(p.From), quoteOrDash(p.To))
	},
	OpAddSeriesMember: func(p Proposal) string {
		if p.To == "" {
			return fmt.Sprintf("add %s to series %s (position unknown)", p.Target, p.Series)
		}
		return fmt.Sprintf("add %s to series %s at position %s", p.Target, p.Series, p.To)
	},
	OpRestatePosition: func(p Proposal) string {
		s := "restate the position"
		if p.Target != "" {
			s += " of " + p.Target
		}
		if p.Series != "" {
			s += " in " + p.Series
		}
		if p.To != "" {
			return s + fmt.Sprintf(": %s -> %s", quoteOrDash(p.From), quoteOrDash(p.To))
		}
		return s + ": " + quoteOrDash(p.From)
	},
	OpDropMembership: func(p Proposal) string {
		if p.Target != "" {
			return fmt.Sprintf("drop %s (position %s) from series %s", p.Target, quoteOrDash(p.From), p.Series)
		}
		return fmt.Sprintf("drop the membership naming %s from series %s", quoteOrDash(p.From), p.Series)
	},
	OpMoveMembership: func(p Proposal) string {
		return fmt.Sprintf("move %s from series %s to %s at position %s", p.Target, p.Series, truncateList(p.Others, 2), quoteOrDash(p.To))
	},
	OpSplitSeries: func(p Proposal) string {
		return fmt.Sprintf("split series %s: move its %s members (%s) to a new series of the same name, %s keeping the slug",
			p.Target, p.To, truncateList(p.Others, 8), p.From)
	},
	OpSetWorkLanguage: func(p Proposal) string {
		return fmt.Sprintf("set the language of %s (and of every recording stating it): %s -> %s", p.Target, quoteOrDash(p.From), quoteOrDash(p.To))
	},
	OpFillField: func(p Proposal) string {
		return fmt.Sprintf("state %s on %s", p.Field, p.Target)
	},
	OpRenameCandidate: func(p Proposal) string {
		s := "candidate for a rename pass: " + p.Target
		if p.To != "" {
			s += " -> " + p.To
		}
		return s
	},
	OpRepointSidecar: func(p Proposal) string {
		return "re-point the works-community sidecar keyed by " + p.Target
	},
	OpAddWorkLink: func(p Proposal) string {
		return fmt.Sprintf("state work %s %s as including %s", p.Target, p.Field, p.To)
	},
	OpAddSeriesLink: func(p Proposal) string {
		return fmt.Sprintf("state series %s %s as including %s", p.Target, p.Field, p.To)
	},
	OpReview: func(Proposal) string { return "review by hand" },
}

// renderAction is the proposal in words. It is the ONE place a Finding's Action
// string is produced (findings.finalize calls it), so the prose and the typed
// proposal cannot disagree.
func renderAction(p Proposal) string {
	if p.Op == OpNone {
		return p.Reason
	}
	phrase, ok := opPhrase[p.Op]
	if !ok {
		// A new op with no phrase: say what it is rather than nothing, so the gap
		// is visible in the report instead of silently rendering blank.
		phrase = func(q Proposal) string { return q.Op }
	}
	s := phrase(p)
	if p.Advisory && p.Op != OpReview {
		s = "do NOT apply mechanically - " + s
	}
	if p.Reason != "" {
		s += " (" + p.Reason + ")"
	}
	return s
}

// classDoc is the one-line description SUMMARY.md prints under each class
// heading. It lives beside the renderer rather than in the detectors so the
// report reads as one document.
var classDoc = map[string]string{
	ClassWorkDup: "near-duplicate work clusters: one book stored as two or more works, usually because a retailer's decorated title minted a second identity. " +
		"Grouped by the cleaned title plus the project's own work-identity rule, so a fork whose author list gained a role-credited contributor still meets its twin. " +
		"Works in incompatible languages are never clustered together.",
	ClassWorkTitle:    "titles still carrying retailer decoration (edition markers, volume markers, an embedded series name, a genre subtitle). Title-only: no slug is ever proposed for change.",
	ClassWorkNoSeries: "works belonging to no series whose title names one, states a volume number, or both.",
	ClassSeriesInteg:  "per-series problems: shared, malformed or non-canonically spelled positions, dangling members, sequence gaps (advisory), an omnibus sitting on a single slot.",
	ClassSeriesDup:    "series whose names are one name spelled two ways.",
	ClassSeriesParen:  "series names carrying a parenthetical. Reported, never merged: a parenthetical is often a deliberate alternative ordering the data model cannot otherwise express.",
	ClassTransLink: "translation links a record's own-language edition decoration states: a series named \"<name> [<Language> Edition]\" " +
		"linked to the one same-author series of that name in another language, and a work titled \"(<Language> Edition)\" in that " +
		"language linked to the one same book in another language. Stated evidence only: exactly one original, or no proposal.",
	ClassLangMix: "series whose members state two or more languages, read against ONE keeper language (the derived one, or for a tie the " +
		"name's edition decoration, else the incumbent half): a member of another language that already sits in a series of its own " +
		"language is dropped, one with exactly one same-name or translation-linked series of its language is moved there, and the " +
		"rest move to a new series of the same name. A coupled member, a narrator contradiction, a tie by incumbency, a " +
		"CONTESTED majority (a keeper member states a translation, the halves share no author, or the principal author writes mostly in " +
		"a minority language) or a conflicting proposal of another class makes a proposal advisory; every mixed series' " +
		"split is also proposed in every other orientation (other-keeper), for a reviewer to accept exactly one. A work whose " +
		"language its narrators contradict, or whose own title says it is in another language (an own-language edition decoration " +
		"of another language, read on a work of ANY tag; or, for EN-TAGGED members only, the language of a non-English mixed series " +
		"the title is written in), is a set-work-language review, never applied mechanically.",
	ClassPersonDup:  "possible duplicate people. ADVISORY throughout, high false-positive rate: two real people can share a name or sit one typo apart, so nothing here proposes an action.",
	ClassRefSidecar: "works-community sidecar hazards: a spoiler-gated sidecar attached to a work that turns out to be one of a duplicate pair, or keyed by a work slug nothing holds.",
	ClassHygiene:    "field-level gaps and slug-convention oddities.",
	ClassLoader:     "pkg/check's own problems and advisories over the same load, carried through unchanged and filed under the loader's own advisory class names.",
}

// sampleCount is how many example records SUMMARY.md prints per subclass. Small
// on purpose: the NDJSON file is the data, the summary is the orientation.
const sampleCount = 3

// summary renders SUMMARY.md. It carries NO timestamp and no absolute path, so
// two runs over one tree produce identical bytes.
func summary(rep *Report) string {
	var b strings.Builder
	b.WriteString("# metaaudit report\n\n")
	b.WriteString("A deterministic, read-only data-quality audit of the `data/` tree. Every class below has a\n")
	b.WriteString("matching `<CLASS>.ndjson` file in this directory, one JSON record per line, sorted so two\n")
	b.WriteString("runs over the same tree produce byte-identical output. Nothing here has been applied to the\n")
	b.WriteString("data: a record's `propose` is a typed repair for a later pass, and its `action` is that\n")
	b.WriteString("same proposal rendered in words - neither is a change that was made.\n\n")

	b.WriteString("## Catalogue\n\n")
	reportdir.Table(&b, "entity", []reportdir.Row{
		{Label: "works", N: rep.Totals.Works},
		{Label: "recordings", N: rep.Totals.Recordings},
		{Label: "people", N: rep.Totals.People},
		{Label: "series", N: rep.Totals.Series},
		{Label: "characters sidecars", N: rep.Totals.Characters},
		{Label: "recaps sidecars", N: rep.Totals.Recaps},
		{Label: "description sidecars", N: rep.Totals.Descriptions},
	})
	fmt.Fprintf(&b, "\nLoader (`pkg/check`): %s, %s.\n\n",
		joinCount(rep.LoaderProblems, "problem"), joinCount(rep.LoaderWarnings, "warning"))

	b.WriteString("## Findings per class\n\n")
	classRows := make([]reportdir.Row, 0, len(classOrder))
	for _, class := range classOrder {
		classRows = append(classRows, reportdir.Row{Label: class, N: rep.class(class).total()})
	}
	reportdir.Table(&b, "class", classRows)
	b.WriteString("\n")

	for _, class := range classOrder {
		c := rep.class(class)
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", class, classDoc[class])
		if c.total() == 0 {
			b.WriteString("No findings.\n\n")
			continue
		}
		subRows := make([]reportdir.Row, 0, 8)
		for _, sc := range c.counts() {
			name := sc.Subclass
			if name == "" {
				name = "(none)"
			}
			subRows = append(subRows, reportdir.Row{Label: name, N: sc.Count})
		}
		reportdir.Table(&b, "subclass", subRows)
		b.WriteString("\n")
		writeSamples(&b, c)
	}

	writeCountOnly(&b, rep)
	return b.String()
}

// writeSamples prints the first records of each subclass, in the file's own
// order, so the summary and the NDJSON agree about what "first" means.
func writeSamples(b *strings.Builder, c *findings) {
	seen := map[string]int{}
	var lines []string
	for _, r := range c.rows {
		if seen[r.Subclass] >= sampleCount {
			continue
		}
		seen[r.Subclass]++
		lines = append(lines, "- `"+r.Subclass+"` "+sampleLine(r))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("Examples:\n\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
}

// sampleLine renders one record as a single readable line.
func sampleLine(r Finding) string {
	parts := []string{"`" + r.Key + "`"}
	p := r.Propose
	switch {
	case p.Field != "" && p.From == "" && p.To == "":
		parts = append(parts, p.Field+" missing")
	case p.Field != "" && p.To == "":
		// A class that flags a value without proposing a replacement - every slug
		// rule, since a slug is identity and a rename is not a mechanical change.
		parts = append(parts, p.Field+": "+quoteOrDash(p.From))
	case p.Field != "":
		parts = append(parts, p.Field+": "+quoteOrDash(p.From)+" -> "+quoteOrDash(p.To))
	case len(r.Works) > 1:
		parts = append(parts, truncateList(refIDs(r.Works), 4))
	case len(r.People) > 1:
		parts = append(parts, truncateList(quotedNames(r.People), 4))
	case len(r.Series) > 1:
		parts = append(parts, truncateList(seriesNames(r.Series), 4))
	case p.From != "":
		parts = append(parts, oneLine(p.From))
	}
	if p.Op != OpNone && p.Op != OpReview {
		parts = append(parts, "`"+p.Op+"`")
	}
	// Parenthesized: && binds tighter than ||, so the unparenthesized form read as
	// "(Target != "" && Op == merge-works) || Op == merge-series" and would have
	// rendered "keep ``" for a series merge with no target.
	if p.Target != "" && (p.Op == OpMergeWorks || p.Op == OpMergeSeries) {
		parts = append(parts, "keep `"+p.Target+"`")
	}
	if p.Advisory {
		parts = append(parts, "ADVISORY")
	}
	return strings.Join(parts, " - ")
}

func refIDs(ws []WorkRef) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ID)
	}
	return out
}

func quotedNames(ps []PersonRef) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, `"`+p.Name+`"`)
	}
	return out
}

func seriesNames(ss []SeriesRef) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, `"`+s.Name+`"`)
	}
	return out
}

func quoteOrDash(s string) string {
	if s == "" {
		return "(unset)"
	}
	return `"` + oneLine(s) + `"`
}

// oneLine keeps a value from breaking the markdown list it sits in.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// writeCountOnly prints the advisories that are counts rather than records: the
// added_at spelling split (documented and expected) and the coverage numbers that
// put the other classes in proportion.
func writeCountOnly(b *strings.Builder, rep *Report) {
	st := rep.Stats
	b.WriteString("## Count-only advisories\n\n")
	b.WriteString("These are not defects to fix one by one, so they have no NDJSON records - the numbers are\n")
	b.WriteString("the whole finding. The `added_at` split is documented and expected: a plain `YYYY-MM-DD`\n")
	b.WriteString("date is what the importer and the intake bot stamp at creation, and a full RFC 3339\n")
	b.WriteString("timestamp is what the storage migration's one-time git-history backfill wrote.\n\n")
	reportdir.Table(b, "measure", []reportdir.Row{
		{Label: "works with `added_at` as a date", N: st.Works.Date},
		{Label: "works with `added_at` as an RFC 3339 timestamp", N: st.Works.Stamp},
		{Label: "works with no `added_at`", N: st.Works.None},
		{Label: "recordings with `added_at` as a date", N: st.Recordings.Date},
		{Label: "recordings with `added_at` as an RFC 3339 timestamp", N: st.Recordings.Stamp},
		{Label: "recordings with no `added_at`", N: st.Recordings.None},
		{Label: "recordings with no runtime", N: st.RecordingsNoRun},
		{Label: "chapters across all recordings", N: st.Chapters},
	})
	b.WriteString("\n")

	lk := rep.Links
	b.WriteString("T-LINK candidates that yielded no proposal. A decorated record is proposed only with EXACTLY ONE\n")
	b.WriteString("original; the rest are counted here rather than guessed at.\n\n")
	reportdir.Table(b, "measure", []reportdir.Row{
		{Label: "series named as a language edition", N: lk.SeriesDecorated},
		{Label: "... naming no same-author series", N: lk.SeriesNoCandidate},
		{Label: "... naming two or more (ambiguous)", N: lk.SeriesAmbiguous},
		{Label: "... whose one candidate fails the language test", N: lk.SeriesLanguageSkips},
		{Label: "works whose title states their own language's edition", N: lk.WorksDecorated},
		{Label: "... with no same-book work in another language", N: lk.WorksNoCandidate},
		{Label: "... with two or more candidate originals (ambiguous)", N: lk.WorksAmbiguous},
	})
	b.WriteString("\n")

	mx := rep.LangMix
	b.WriteString("L-MIX in proportion: the mixed-language series, the minority memberships behind the class's\n")
	b.WriteString("proposals and why some are advisory, and the works whose recordings state another language\n")
	b.WriteString("(the recording relocation pass's population; the ones whose every recording states one other\n")
	b.WriteString("language are the set-work-language candidates where the narrators agree).\n\n")
	reportdir.Table(b, "measure", []reportdir.Row{
		{Label: "series whose members state two or more languages", N: mx.MixedSeries},
		{Label: "... tied (no strict plurality)", N: mx.TieSeries},
		{Label: "... tied, decided by the name's edition decoration", N: mx.TieByDecoration},
		{Label: "memberships outside the keeper language", N: mx.Minority},
		{Label: "... coupled (carrying a recording in the keeper language)", N: mx.Coupled},
		{Label: "... whose narrators contradict the member's language", N: mx.NarratorContradicted},
		{Label: "... withheld by another class's proposal on the same record", N: mx.CrossClass},
		{Label: "majority series whose keeper is contested", N: mx.Contested},
		{Label: "... a keeper-language member states a translation (stated)", N: mx.ContestedStated},
		{Label: "... the keeper and minority halves share no author (collision)", N: mx.ContestedCollision},
		{Label: "... the principal author writes mostly in a minority language (home)", N: mx.ContestedHome},
		{Label: "split proposals in another orientation", N: mx.OtherKeeperSplits},
		{Label: "recordings stating a language their work does not", N: mx.CrossRecordings},
		{Label: "... over this many works", N: mx.CrossWorks},
		{Label: "works whose every recording states one other language", N: mx.AllOther},
		{Label: "... whose narrators contradict the work's language", N: mx.AllOtherContradicted},
		{Label: "works whose title states another language's edition than their tag", N: mx.TitleEdition},
		{Label: "... withheld: the title names a language (a course)", N: mx.TitleCourse},
		{Label: "en-tagged works in a non-English mixed series titled in its language (each work once)", N: mx.TitleSeries},
		{Label: "set-work-language proposals from title evidence alone (title-language)", N: mx.TitleProposals},
	})
	b.WriteString("\n")

	writeReviewedSummary(b, rep.Reviewed)
}

func writeReviewedSummary(b *strings.Builder, t reviewedTally) {
	b.WriteString("Reviewed decisions (`" + reviewedPath + "`), matched against fresh proposals after all classes.\n")
	b.WriteString("Accept promotes to mechanical; reject makes advisory. No-op decisions leave the status unchanged.\n")
	b.WriteString("Assert sources a mechanical proposal no detector makes (subclass `asserted`); one a detector already\n")
	b.WriteString("makes is taken as an acceptance and reported redundant, to be rewritten as one.\n")
	b.WriteString("Refused acceptances stay advisory; STALE decisions match no fresh proposal and are never applied.\n\n")
	counts := map[outcomeStatus]int{}
	for _, o := range t.All {
		counts[o.Status]++
	}
	reportdir.Table(b, "measure", []reportdir.Row{
		{Label: "reviewed decisions on the list", N: t.Entries()},
		{Label: "... accepted (made mechanical)", N: counts[statusAccepted]},
		{Label: "... rejected (made advisory)", N: counts[statusRejected]},
		{Label: "... asserted (sourced a mechanical proposal)", N: counts[statusAsserted]},
		{Label: "... asserted, already proposed (redundant)", N: counts[statusRedundant]},
		{Label: "... no-op", N: counts[statusNoOp]},
		{Label: "... acceptances or assertions refused", N: counts[statusRefused]},
		{Label: "... rejections withholding an assertion", N: counts[statusWithholds]},
		{Label: "... matching no proposal (STALE)", N: counts[statusStale]},
	})
	b.WriteString("\n")
	for _, o := range t.All {
		if !o.Status.listed() {
			continue
		}
		fmt.Fprintf(b, "- %s `%s`: %s", o.Status, decisionIdentity(o.Entry), o.Entry.Reason)
		if o.Why != "" {
			fmt.Fprintf(b, "; %s", o.Why)
		}
		b.WriteString("\n")
	}
	for _, o := range t.Stale() {
		fmt.Fprintf(b, "- STALE `%s`: %s: %s", decisionIdentity(o.Entry), o.Entry.Decision, o.Entry.Reason)
		if o.Why != "" {
			fmt.Fprintf(b, "; %s", o.Why)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func decisionIdentity(r reviewedDecision) string {
	raw, _ := json.Marshal(r.proposal())
	return string(raw)
}
