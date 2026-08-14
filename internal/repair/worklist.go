package repair

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
)

// worklist.go decides WHICH of the fresh audit's proposals a run takes on.
//
// The central rule lives here: a `-report` directory is a FILTER, never a source of
// instructions. Every proposal this pass applies comes from the fresh, in-process
// audit over the tree being modified; the file can only narrow that set, and a record
// in the file that the fresh run no longer makes the same way is REFUSED and named.
// So a report is safe to keep, safe to hand to a colleague, and safe to re-run
// against a tree that has moved on - it cannot resurrect a decision the data no
// longer supports.

// filter is the requested subset of the fresh audit's proposals.
type filter struct {
	ops        map[string]bool
	subclasses map[string]bool
	// only holds the keys named explicitly, and matched records which of them
	// selected something - a key that matches nothing is a request that went
	// nowhere, so it is reported rather than silently dropped.
	only    map[string]bool
	matched map[string]bool
	// worklist maps a finding identity to the PROPOSAL the report file recorded for
	// it. nil when no report directory was given, which is "whatever the fresh
	// audit proposes".
	worklist map[string]audit.Proposal
	// records is how many appliable, non-advisory records the report held.
	records int
}

// candidate is one fresh finding selected for planning.
type candidate struct {
	class string
	fd    audit.Finding
}

// key identifies the proposal in a message: the class and the finding key, which is
// what the report files are indexed by.
func (c candidate) key() string { return c.class + " " + c.fd.Key }

// findingID is a finding's identity across two runs of the audit: the class, the
// subclass, the grouping key, and the field/from a single-field op names (one series
// yields several S-INTEGRITY records that differ only in those).
//
// It deliberately does NOT include the values a run might legitimately change - the
// op, the target, the others, the To - because those are what the staleness check
// compares. An identity that included them would make a changed proposal read as a
// different one and disappear silently instead of being refused.
func findingID(class string, fd audit.Finding) string {
	return strings.Join([]string{class, fd.Subclass, fd.Key, fd.Propose.Field, fd.Propose.From}, "\x00")
}

// sameProposal reports whether two proposals for one identity say the same thing.
// Reason is excluded: it is prose for a human, and rewording a sentence must not
// invalidate a worklist a maintainer has already reviewed.
func sameProposal(a, b audit.Proposal) bool {
	return a.Op == b.Op && a.Target == b.Target && a.Series == b.Series &&
		a.Field == b.Field && a.From == b.From && a.To == b.To &&
		a.Advisory == b.Advisory && slices.Equal(a.Others, b.Others)
}

// loadFilter builds the run's filter from the options: the op and subclass sets, the
// explicit keys, and the report directory's worklist.
func loadFilter(opts Options) (*filter, error) {
	f := &filter{matched: map[string]bool{}}
	if len(opts.Ops) > 0 {
		f.ops = map[string]bool{}
		for _, op := range opts.Ops {
			if !appliableOps[op] {
				return nil, fmt.Errorf("repair: --op %q is not one this pass can apply (want one of %s)",
					op, joinList(AppliableOps()))
			}
			f.ops[op] = true
		}
	}
	if len(opts.Subclasses) > 0 {
		f.subclasses = map[string]bool{}
		for _, s := range opts.Subclasses {
			f.subclasses[s] = true
		}
	}
	if opts.OnlyFile != "" {
		keys, err := readKeys(opts.OnlyFile)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("repair: --only %s names no keys", opts.OnlyFile)
		}
		f.only = keys
	}
	if opts.ReportDir != "" {
		wl, err := readWorklist(opts.ReportDir)
		if err != nil {
			return nil, err
		}
		f.worklist, f.records = wl, len(wl)
	}
	return f, nil
}

// readKeys reads a key list: one per line, blank lines and # comments skipped.
func readKeys(path string) (map[string]bool, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("repair: read --only %s: %w", path, err)
	}
	defer fh.Close() //nolint:errcheck // read-only
	out := map[string]bool{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("repair: read --only %s: %w", path, err)
	}
	return out, nil
}

// readWorklist reads a metaaudit report directory: every class file it holds, keyed
// by finding identity. Only appliable, non-advisory records are kept - the rest of a
// report is a reading list, and carrying 200k advisory records as "requested" would
// make every one of them a refusal.
//
// The file names come from audit.ClassFile over audit.Classes(), so the reader and
// the writer of a report share one definition of the directory's shape. A class file
// that is absent is skipped (an older report may predate a class); a directory
// holding none of them is not a report directory at all.
func readWorklist(dir string) (map[string]audit.Proposal, error) {
	out := map[string]audit.Proposal{}
	isReport := false
	for _, class := range audit.Classes() {
		path := filepath.Join(dir, audit.ClassFile(class))
		fh, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("repair: read report %s: %w", path, err)
		}
		isReport = true
		err = scanNDJSON(fh, path, func(fd audit.Finding) {
			if !appliableOps[fd.Propose.Op] || fd.Propose.Advisory {
				return
			}
			out[findingID(class, fd)] = fd.Propose
		})
		_ = fh.Close()
		if err != nil {
			return nil, err
		}
	}
	if !isReport {
		return nil, fmt.Errorf("repair: %s holds none of the audit's class files (%s and friends): "+
			"point -report at a metaaudit output directory", dir, audit.ClassFile(audit.Classes()[0]))
	}
	return out, nil
}

// scanNDJSON reads one record per line. The buffer is raised because a W-DUP record
// carrying a large cluster is a long line, and the default 64KB scanner would fail
// on it with "token too long" - which would read as a corrupt report.
func scanNDJSON(fh *os.File, path string, fn func(audit.Finding)) error {
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 256<<10), 8<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var fd audit.Finding
		if err := json.Unmarshal([]byte(raw), &fd); err != nil {
			return fmt.Errorf("repair: %s line %d: %w", path, line, err)
		}
		fn(fd)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("repair: %s: %w", path, err)
	}
	return nil
}

// selectProposals turns the fresh report into the ordered list of proposals to plan,
// recording every one it declines along the way.
//
// The order is the REPORT's: classes in audit.Classes() order, findings in the order
// the class holds them (which is the order the NDJSON file carries). That is what
// makes --limit a chunking tool rather than a lottery - two runs with the same limit
// over the same tree consider exactly the same records.
func (rn *runner) selectProposals(fresh *audit.Report) []candidate {
	f := rn.filter
	var out []candidate
	// handled marks the identities the fresh pass has already decided about, so the
	// worklist sweep below reports only what the fresh run did not produce at all.
	handled := map[string]bool{}

	for _, class := range audit.Classes() {
		for _, fd := range fresh.Findings(class) {
			p := fd.Propose
			if !appliableOps[p.Op] {
				rn.rep.Unsupported++
				continue
			}
			if p.Advisory {
				rn.rep.Advisory++
				continue
			}
			id := findingID(class, fd)
			c := candidate{class: class, fd: fd}
			if !f.selects(fd) {
				rn.rep.NotRequested++
				continue
			}
			if f.worklist != nil {
				want, inList := f.worklist[id]
				if !inList {
					rn.rep.NotInWorklist++
					continue
				}
				handled[id] = true
				if !sameProposal(want, p) {
					rn.refuse(c, CatStaleProposal, fmt.Sprintf(
						"the worklist asks for %s and the fresh audit over this tree now proposes %s: the tree has moved, so the "+
							"reviewed decision no longer describes it - re-run metaaudit and review the new record",
						renderProposal(want), renderProposal(p)))
					continue
				}
			}
			if rn.opts.Limit > 0 && len(out) >= rn.opts.Limit {
				rn.rep.BeyondLimit++
				continue
			}
			out = append(out, c)
		}
	}
	rn.rep.Considered = len(out)
	rn.refuseStaleWorklist(fresh, handled)
	rn.refuseUnmatchedKeys()
	return out
}

// refuseStaleWorklist names every worklist record the fresh run no longer proposes at
// all, or now marks advisory. This is the case the whole design exists for: the
// reviewed report says "merge these two", the tree has since changed, and applying it
// would act on a decision nothing supports any more.
func (rn *runner) refuseStaleWorklist(fresh *audit.Report, handled map[string]bool) {
	if rn.filter.worklist == nil {
		return
	}
	// Indexed by the WORKLIST's ids, not by every finding the fresh run made: this sweep
	// only ever asks about a record the file holds, and a report of 280k findings would
	// otherwise be copied into a map to answer a few hundred questions.
	byID := make(map[string]candidate, len(rn.filter.worklist))
	for _, class := range audit.Classes() {
		for _, fd := range fresh.Findings(class) {
			id := findingID(class, fd)
			if _, wanted := rn.filter.worklist[id]; wanted {
				byID[id] = candidate{class: class, fd: fd}
			}
		}
	}
	for _, id := range rawentry.SortedKeys(rn.filter.worklist) {
		if handled[id] {
			continue
		}
		want := rn.filter.worklist[id]
		c, still := byID[id]
		if !still {
			class, subclass, key := splitID(id)
			c = candidate{class: class, fd: audit.Finding{Subclass: subclass, Key: key, Propose: want}}
			if !rn.filter.selects(c.fd) {
				continue
			}
			rn.refuse(c, CatStaleProposal,
				"the worklist asks for "+renderProposal(want)+" and the fresh audit over this tree does not report this finding "+
					"at all: it has already been repaired, or the records it named have changed")
			continue
		}
		if !rn.filter.selects(c.fd) {
			continue
		}
		if c.fd.Propose.Advisory {
			rn.refuse(c, CatStaleProposal,
				"the worklist asks for "+renderProposal(want)+" and the fresh audit now marks it advisory: "+c.fd.Propose.Reason)
		}
	}
}

// refuseUnmatchedKeys reports every --only key that selected nothing. An explicit
// request that goes nowhere is the one filter miss worth naming: a mistyped key would
// otherwise read as "there was nothing to do".
func (rn *runner) refuseUnmatchedKeys() {
	f := rn.filter
	if f.only == nil {
		return
	}
	for _, key := range rawentry.SortedKeys(f.only) {
		if f.matched[key] {
			continue
		}
		rn.rep.Refused = append(rn.rep.Refused, Refusal{
			Category: CatNotProposed, Key: key,
			Reason: "named in --only, but the fresh audit proposes nothing non-advisory and in scope for it",
		})
	}
}

// selects reports whether the filters admit a finding, and marks the --only key that
// admitted it.
func (f *filter) selects(fd audit.Finding) bool {
	if f.ops != nil && !f.ops[fd.Propose.Op] {
		return false
	}
	if f.subclasses != nil && !f.subclasses[fd.Subclass] {
		return false
	}
	if f.only == nil {
		return true
	}
	// A key may be the finding's own key or any slug the proposal names, because
	// that is how a maintainer reads a report: they have a work id in front of them,
	// not a cluster key.
	hit := ""
	for _, k := range append([]string{fd.Key, fd.Propose.Target, fd.Propose.Series}, fd.Propose.Others...) {
		if k != "" && f.only[k] {
			hit = k
			break
		}
	}
	if hit == "" {
		return false
	}
	f.matched[hit] = true
	return true
}

// splitID reads back the identity components a message and the filters need when the
// fresh run no longer holds the finding at all - so a --subclass filter judges a stale
// worklist record by the same subclass it would have judged the live one by.
func splitID(id string) (class, subclass, key string) {
	parts := strings.SplitN(id, "\x00", 4)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// renderProposal states a proposal in one clause, for a refusal message. It is the
// machine-readable fields, not the audit's prose: the point of a staleness refusal is
// to show the two decisions side by side.
func renderProposal(p audit.Proposal) string {
	var b strings.Builder
	b.WriteString(p.Op)
	if p.Target != "" {
		b.WriteString(" " + p.Target)
	}
	if len(p.Others) > 0 {
		b.WriteString(" <- " + joinList(p.Others))
	}
	if p.Series != "" {
		b.WriteString(" in " + p.Series)
	}
	if p.Field != "" {
		fmt.Fprintf(&b, " %s %q -> %q", p.Field, p.From, p.To) //nolint:errcheck // a strings.Builder never fails
	}
	if p.Advisory {
		b.WriteString(" (advisory)")
	}
	return b.String()
}
