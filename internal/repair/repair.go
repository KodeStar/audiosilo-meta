// Package repair is metarepair's business logic: it applies internal/audit's
// NON-ADVISORY proposals to the data tree.
//
// It is the write half of the audit, and the most dangerous code in this
// repository: merging duplicate works DELETES records from a public database, and a
// wrong merge is not a bad report, it is destroyed data. Four rules carry that
// weight, and every one of them is a property of the design rather than a matter of
// care:
//
// 1. THE WORKLIST IS A FILTER; THE LIVE TREE IS THE TRUTH. A run re-runs the audit
// IN PROCESS over the very tree it is about to modify (audit.Analyze over one
// check.LoadStore) and applies only what the FRESH run still proposes as
// non-advisory. A `-report` directory only narrows that set: a record in the file
// that the fresh run no longer proposes - because the tree moved, or a veto now
// trips, or the proposal changed - is refused and named, never applied. This is what
// makes the tool idempotent by construction rather than by a re-derivation: a second
// run over repaired data proposes nothing, so it does nothing.
//
// 2. THE WHOLE RUN IS PLANNED BEFORE A BYTE IS WRITTEN, internal/remediate's and
// internal/migrate's discipline, with the grain moved to the PROPOSAL: each is
// planned inside a txn against the plan so far and either commits whole or is
// discarded whole and named in the report. Nothing reaches disk until every
// proposal has been planned, and every write goes through pkg/pack.
//
// 3. A MERGE LOSES NOTHING. Recordings move (merged with a colliding sibling only on
// the importer's own same-production evidence, else re-keyed through the project's
// numbered-slug chain and moved intact), works-community sidecars move, series
// memberships are re-pointed, identifiers and provenance are unioned, and every
// retired slug gets a tombstone in data/redirects.json (pkg/redirects.Add, which
// collapses chains in both directions) - because a slug is public API. Where a
// mechanical rule cannot decide, the cluster is REFUSED and named: both sides
// carrying the same sidecar member, two positions for one book in one series, a
// record an earlier proposal retired.
//
// 4. A RED TREE IS A FAILED RUN. After writing, the pass heals and canonicalizes the
// tree (internal/format, so metafmt's rules are not restated here) and then loads it
// again through pkg/check; problems make the run exit non-zero with them named. It
// also refuses to WRITE into a tree that does not validate in the first place, or one
// with uncommitted changes - git is the recovery story, exactly as it is for
// metamigrate.
//
// DRY RUN IS THE DEFAULT. Without --write the plan is composed in full, reported,
// and thrown away; the report (an NDJSON pair plus a PR-ready summary) is the review
// artifact a maintainer reads before anything is applied.
package repair

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/format"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// writeFamilies are the families a repair writes. people is deliberately absent:
// P-DUP is advisory throughout, so nothing here ever merges a person.
var writeFamilies = []pack.Family{pack.FamilyWorks, pack.FamilyWorksCommunity, pack.FamilySeries}

// appliableOps are the ops this pass can carry out. Every other op the audit emits
// (review, drop-membership, rename-candidate, repoint-sidecar) is advisory by
// construction and is not a gap here: each names a decision a rule may not make.
var appliableOps = map[string]bool{
	audit.OpMergeWorks:      true,
	audit.OpMergeSeries:     true,
	audit.OpRetitle:         true,
	audit.OpAddSeriesMember: true,
	audit.OpRestatePosition: true,
	audit.OpFillField:       true,
}

// AppliableOps returns the ops --op accepts, sorted. The CLI prints it, so the flag's
// help and the set the planner switches on have one definition.
func AppliableOps() []string { return sortedKeys(appliableOps) }

// The refusal categories. A refusal is a proposal this pass declined to apply, and
// the category is what a maintainer triages by.
const (
	catStaleProposal      = "stale-proposal"
	catUnaddressableTitle = "title-carries-no-identity"
	catNotProposed        = "not-proposed"
	catMalformed          = "malformed-proposal"
	catMissing            = "record-missing"
	catRetired            = "record-retired"
	catSidecarCollision   = "sidecar-member-collision"
	catPositionConflict   = "series-position-conflict"
	catRecordingKey       = "recording-key-exhausted"
	catStaleValue         = "stale-value"
	catNoValue            = "no-value-stated"
	catRedirect           = "redirect-refused"
)

// Options configure a run.
type Options struct {
	// DataDir is the data root. It is only written with Write set.
	DataDir string
	// ReportDir is an optional metaaudit report directory whose records form the
	// WORKLIST: only proposals it holds are considered. Empty means "whatever the
	// fresh audit proposes".
	ReportDir string
	// OutDir is where the apply-report is written. Empty writes no files; the
	// summary still goes to the caller's writer. It may not sit inside DataDir.
	OutDir string
	// Ops restricts the run to these ops (see AppliableOps). Empty means all.
	Ops []string
	// Subclasses restricts the run to these audit subclasses. Empty means all.
	Subclasses []string
	// OnlyFile is a file of explicit keys (one per line, # comments allowed): a
	// finding key, or a work/series slug the proposal names. Only proposals it
	// names are considered, and a key that matches nothing is reported.
	OnlyFile string
	// Limit caps how many matching proposals are planned, in the report's
	// deterministic order - which is how a wave is chunked. 0 means no cap.
	// Refusals count against it: it bounds what the run considers, so two runs
	// with the same limit consider the same records.
	Limit int
	// Write applies the plan. The zero value composes it, reports it and touches
	// nothing.
	Write bool
}

// Applied is one proposal this run carried out.
type Applied struct {
	Class    string   `json:"class"`
	Subclass string   `json:"subclass,omitempty"`
	Key      string   `json:"key"`
	Op       string   `json:"op"`
	Target   string   `json:"target,omitempty"`
	Others   []string `json:"others,omitempty"`
	Series   string   `json:"series,omitempty"`
	Field    string   `json:"field,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	// Notes are what the change actually did, record by record: which recordings
	// moved, which were merged or re-keyed, which sidecar members travelled, which
	// slugs were tombstoned. It is the audit trail a reviewer reads.
	Notes []string `json:"notes,omitempty"`
}

// Refusal is one proposal this run declined, with the reason a human needs.
type Refusal struct {
	Category string   `json:"category"`
	Class    string   `json:"class,omitempty"`
	Subclass string   `json:"subclass,omitempty"`
	Key      string   `json:"key"`
	Op       string   `json:"op,omitempty"`
	Target   string   `json:"target,omitempty"`
	Others   []string `json:"others,omitempty"`
	Reason   string   `json:"reason"`
}

// Report is everything a run did or declined to do.
type Report struct {
	DataDir string
	// Wrote reports whether the plan was applied.
	Wrote bool
	// Considered is how many fresh, non-advisory, in-scope proposals the run took
	// on (the number the limit bounds).
	Considered int
	// Advisory, Unsupported and NotRequested are what the fresh run proposed and
	// this run did not take: a proposal a mechanical pass must not apply, an op
	// this pass cannot carry out, and one the filters excluded.
	Advisory    int
	Unsupported int
	// The remaining counts are split so a summary can say WHY a proposal was left
	// out: the filters, the limit, or the worklist.
	NotRequested    int
	BeyondLimit     int
	NotInWorklist   int
	WorklistRecords int

	Applied []Applied
	Refused []Refusal

	// Totals are the catalogue's sizes at load, the denominators the counts are
	// read against.
	Totals audit.Totals
	// LoaderProblems is how many problems pkg/check reported on the tree BEFORE
	// the run. A write refuses on any; a dry run says so and carries on.
	LoaderProblems int
	// Redirects is how many tombstones the run recorded.
	Redirects int

	// The write phase's own accounting.
	PacksWritten []string
	PacksRemoved []string
	Formatted    []string
	HealedWrote  []string
	HealedGone   []string
	// PostProblems are the problems pkg/check reported AFTER the write, which make
	// the run fail.
	PostProblems []string
}

// runner is one run's state: the options, the plan being composed, the report being
// filled, and the worklist filter.
type runner struct {
	opts   Options
	plan   *plan
	rep    *Report
	filter *filter
	// fatal is an error that is not a refusal (I/O, an unreadable entry). It stops
	// the planning loop, and a run that has one writes nothing.
	fatal error
}

// refusal is a proposal this pass declines, as an error. It is distinct from a
// plain error on purpose: a refusal is a judgement about DATA and belongs in the
// report, while an error is a failure of the RUN and aborts it before anything is
// written.
type refusal struct {
	category string
	reason   string
}

func (r *refusal) Error() string { return r.category + ": " + r.reason }

func refusef(category, format string, a ...any) error {
	return &refusal{category: category, reason: fmt.Sprintf(format, a...)}
}

// Run plans the repair over opts.DataDir and, with opts.Write, applies it.
func Run(opts Options) (*Report, error) {
	if err := checkOutDir(opts); err != nil {
		return nil, err
	}
	f, err := loadFilter(opts)
	if err != nil {
		return nil, err
	}
	if opts.Write {
		// git is the only recovery from a partial write, exactly as it is for
		// metamigrate: this pass deletes records.
		if err := refuseUncommitted(opts.DataDir); err != nil {
			return nil, err
		}
	}

	store, err := pack.OpenFor(opts.DataDir, writeFamilies...)
	if err != nil {
		return nil, err
	}
	// ONE walk of the tree serves both the validation and the audit: the store's
	// own, borrowed by check.LoadStore, whose Result the detectors then read.
	res := check.LoadStore(store)
	if res.Catalog == nil {
		return nil, fmt.Errorf("repair: %s could not be loaded: %s", opts.DataDir, firstProblem(res))
	}
	table, err := redirects.Load(opts.DataDir)
	if err != nil {
		return nil, err
	}

	fresh := audit.Analyze(res)
	rep := &Report{
		DataDir:         opts.DataDir,
		Totals:          fresh.Totals,
		LoaderProblems:  len(res.Problems),
		WorklistRecords: f.records,
	}
	if len(res.Problems) > 0 && opts.Write {
		return rep, fmt.Errorf("repair: %s has %d validation problem(s) - a record the loader dropped is invisible to the plan, "+
			"so run `go run ./cmd/metacheck` and fix them before writing:\n  %s",
			opts.DataDir, len(res.Problems), firstProblem(res))
	}

	rn := &runner{opts: opts, plan: newPlan(store, res.Catalog, table), rep: rep, filter: f}
	for _, c := range rn.selectProposals(fresh) {
		if rn.fatal != nil {
			break
		}
		rn.planOne(c)
	}
	if rn.fatal != nil {
		return rep, rn.fatal
	}
	sortRefusals(rep.Refused)
	rep.Redirects = countTombs(rep.Applied)

	if opts.Write {
		if err := rn.apply(store); err != nil {
			return rep, err
		}
	}
	if err := rep.writeFiles(opts.OutDir); err != nil {
		return rep, err
	}
	return rep, nil
}

// planOne plans one selected proposal, recording an Applied or a Refusal.
func (rn *runner) planOne(c candidate) {
	t := rn.plan.begin()
	p := c.fd.Propose
	var err error
	switch p.Op {
	case audit.OpMergeWorks:
		err = rn.mergeWorks(t, c.fd)
	case audit.OpMergeSeries:
		err = rn.mergeSeries(t, c.fd)
	case audit.OpRetitle:
		err = rn.retitleWork(t, c.fd)
	case audit.OpRestatePosition:
		err = rn.restatePosition(t, c.fd)
	case audit.OpAddSeriesMember:
		err = rn.addSeriesMember(t, c.fd)
	case audit.OpFillField:
		err = rn.fillField(t, c.fd)
	default:
		// Unreachable: selectProposals only yields appliableOps. Loud rather than
		// silent, because a new op added to that map and not to this switch would
		// otherwise be a proposal counted as applied and never carried out.
		err = refusef(catMalformed, "op %q has no planner", p.Op)
	}
	if err == nil {
		if cerr := t.commit(c.key()); cerr != nil {
			// The only thing commit judges is the tombstone table, and it judges it
			// over a copy - so a refusal here has changed nothing.
			err = refusef(catRedirect, "%v", cerr)
		}
	}
	if err != nil {
		var ref *refusal
		if errors.As(err, &ref) {
			rn.refuse(c, ref.category, ref.reason)
			return
		}
		rn.fatal = err
		return
	}
	rn.rep.Applied = append(rn.rep.Applied, Applied{
		Class: c.class, Subclass: c.fd.Subclass, Key: c.fd.Key,
		Op: p.Op, Target: p.Target, Others: p.Others, Series: p.Series,
		Field: p.Field, From: p.From, To: p.To,
		Notes: t.notes,
	})
}

// refuse records a declined proposal.
func (rn *runner) refuse(c candidate, category, reason string) {
	rn.rep.Refused = append(rn.rep.Refused, Refusal{
		Category: category, Class: c.class, Subclass: c.fd.Subclass, Key: c.fd.Key,
		Op: c.fd.Propose.Op, Target: c.fd.Propose.Target, Others: c.fd.Propose.Others,
		Reason: reason,
	})
}

// apply writes the plan, heals the tree and validates it. A tree that does not
// validate afterwards is a FAILED run: it exits non-zero with the problems named,
// so a red tree is never left quietly behind.
func (rn *runner) apply(store *pack.Store) error {
	for _, v := range []*view{rn.plan.works, rn.plan.community, rn.plan.series} {
		if err := v.queue(); err != nil {
			return err
		}
	}
	written, err := store.Flush()
	if err != nil {
		return err
	}
	rn.rep.Wrote = true
	rn.rep.PacksWritten, rn.rep.PacksRemoved = written.Wrote, written.Deleted

	// The tombstones go in AFTER the packs: a table naming a slug that was never
	// retired is a problem pkg/check reports, and this order means a failed flush
	// cannot leave one behind.
	if rn.plan.redirectsChanged {
		if err := redirects.Write(rn.opts.DataDir, rn.plan.redirects); err != nil {
			return err
		}
	}
	// metafmt's own pass, not a restatement of it: canonical form plus the
	// self-healing placement work a deletion can leave due (a rebind of the lowest
	// pack, a split).
	fr, err := format.Write(rn.opts.DataDir)
	if err != nil {
		return err
	}
	rn.rep.Formatted, rn.rep.HealedWrote, rn.rep.HealedGone = fr.Formatted, fr.Wrote, fr.Deleted
	if fr.NeedsHuman() {
		return fmt.Errorf("repair: the tree needs a human after the write: %s (%s)", fr.Summary(), fr.Advice())
	}
	res := check.Load(rn.opts.DataDir)
	if len(res.Problems) > 0 {
		for _, p := range res.Problems {
			rn.rep.PostProblems = append(rn.rep.PostProblems, p.String())
		}
		return fmt.Errorf("repair: %s does not validate after the repair (%d problem(s)); the change is on disk, so recover with "+
			"`git checkout -- %s` and report the first one:\n  %s",
			rn.opts.DataDir, len(res.Problems), rn.opts.DataDir, res.Problems[0].String())
	}
	return nil
}

// checkOutDir refuses an apply-report directory inside the data root, for the reason
// the audit refuses one: a report that lands in the tree becomes a file metafmt and
// metacheck then judge as if it were a record.
func checkOutDir(opts Options) error {
	if opts.OutDir == "" {
		return nil
	}
	data, err := resolvePath(opts.DataDir)
	if err != nil {
		return fmt.Errorf("repair: resolve %s: %w", opts.DataDir, err)
	}
	out, err := resolvePath(opts.OutDir)
	if err != nil {
		return fmt.Errorf("repair: resolve %s: %w", opts.OutDir, err)
	}
	rel, err := filepath.Rel(data, out)
	if err != nil {
		return nil // different volumes, so certainly not inside
	}
	if rel == "." || !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("repair: the report directory %s is inside the data root %s: point -o somewhere else",
			opts.OutDir, opts.DataDir)
	}
	return nil
}

// resolvePath is the absolute, symlink-resolved form of a path, resolved as far as it
// exists - the report directory is normally created by the run, so "not there yet"
// must not be an error.
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest, cur := "", abs
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// refuseUncommitted rejects a data tree with uncommitted changes when the run is
// about to write. This pass DELETES records, so `git checkout -- data` is the whole
// recovery story; a tree that is already dirty has no such net, and an
// already-half-applied run shows up here rather than being applied on top of.
//
// A tree that is not in a git repository at all is allowed: fixtures and scratch
// trees are exactly that, and there is nothing to restore from either way.
func refuseUncommitted(dataDir string) error {
	if _, err := gitOutput(dataDir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil
	}
	status, err := gitOutput(dataDir, "status", "--porcelain", "--", ".")
	if err != nil {
		return err
	}
	if status == "" {
		return nil
	}
	return fmt.Errorf("repair: %s has uncommitted changes:\n%s\n"+
		"commit or stash them first. This pass deletes records, so a committed tree is what makes "+
		"`git checkout -- %s` a complete recovery", dataDir, status, dataDir)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func firstProblem(res check.Result) string {
	if len(res.Problems) == 0 {
		return "no catalog returned and no problem reported"
	}
	return res.Problems[0].String()
}

// sortRefusals orders refusals by category, then key, then reason, so two runs
// report the same list in the same order.
func sortRefusals(rs []Refusal) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Category != rs[j].Category {
			return rs[i].Category < rs[j].Category
		}
		if rs[i].Key != rs[j].Key {
			return rs[i].Key < rs[j].Key
		}
		return rs[i].Reason < rs[j].Reason
	})
}

// countTombs counts the redirects the applied proposals recorded: one per retired
// slug, which is one per loser of a merge.
func countTombs(applied []Applied) int {
	n := 0
	for _, a := range applied {
		switch a.Op {
		case audit.OpMergeWorks, audit.OpMergeSeries:
			n += len(a.Others)
		}
	}
	return n
}

// joinList renders a string list for a note or a message.
func joinList(ss []string) string { return strings.Join(ss, ", ") }
