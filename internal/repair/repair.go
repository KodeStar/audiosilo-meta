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
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// writeFamilies are the families a repair writes. people is deliberately absent:
// P-DUP is advisory throughout, so nothing here ever merges a person.
var writeFamilies = []pack.Family{pack.FamilyWorks, pack.FamilyWorksCommunity, pack.FamilySeries}

// openCommunity opens Options.CommunityDir as a READ-ONLY community root, or
// returns nil when the flag was not given. Nothing here ever writes to it: it is
// opened as a store only because a store is what answers "give me this family's
// entry for this slug", and no Upsert, Delete or Flush is reachable from the view
// it backs (see newCommunityView and view.queue).
//
// It refuses two things at the door, before anything is planned:
//
//   - a root the tree ALREADY holds the family for. Two answers to one question is
//     not a mode this pass has; the operator meant --profile core.
//   - a root carrying no works-community entries. pack.ListProfile tolerates a
//     missing family root by design, so a directory that simply is not a community
//     checkout - the community repository's TOP LEVEL rather than its data/, the
//     mistake this flag invites - would otherwise answer "no sidecars anywhere" for
//     every cluster in the wave. That is the exact blindness the flag exists to
//     end, arrived at while looking like it had been fixed. It is metabuild's rule
//     for --community, in the terms this pass can check it in.
func openCommunity(opts Options) (*pack.Store, error) {
	if opts.CommunityDir == "" {
		return nil, nil
	}
	if opts.Profile.Has(pack.FamilyWorksCommunity) {
		return nil, fmt.Errorf("repair: --community names a second root, but %s already holds the works-community family "+
			"under the %s profile: pass --profile core, or drop --community", opts.DataDir, opts.Profile)
	}
	s, err := pack.OpenForProfile(opts.CommunityDir, pack.ProfileCommunity, pack.FamilyWorksCommunity)
	if err != nil {
		return nil, fmt.Errorf("repair: open community root: %w", err)
	}
	if n := len(s.Tree(pack.FamilyWorksCommunity).Packs()); n == 0 {
		return nil, fmt.Errorf("repair: %s holds no works-community packs: point --community at the community checkout's "+
			"data/ directory, or omit it (and accept that every merge is refused, which is the safe reading)",
			opts.CommunityDir)
	}
	return s, nil
}

// writeFamiliesIn narrows writeFamilies to the ones the root's tree profile
// actually holds. It is not a convenience: pack.OpenForProfile REFUSES a named
// family the profile disclaims, and rightly - a writer must learn at the door
// that a record has no home in this root. Since the community-repo split the CC
// BY-SA layer lives in KodeStar/audiosilo-meta-community, so a `core` run writes
// works and series and never addresses works-community. Nothing else changes: no
// sidecar loads from a core tree, so the sidecar-moving arm of a merge has
// nothing to move, and under the default profile the list is unnarrowed and this
// pass behaves exactly as it always did over a whole-database tree.
func writeFamiliesIn(p pack.Profile) []pack.Family {
	out := make([]pack.Family, 0, len(writeFamilies))
	for _, f := range writeFamilies {
		if p.Has(f) {
			out = append(out, f)
		}
	}
	return out
}

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
func AppliableOps() []string { return rawentry.SortedKeys(appliableOps) }

// Category is why a proposal was declined. It is EXPORTED and TYPED because it is the
// machine-readable field of a published artifact: REFUSED.ndjson is what a triage script
// groups by, what a wave's follow-up worklist is filtered on, and what a later prevention
// layer will name when it says which defect class it closed. A consumer naming a category
// should name a constant, not a string literal a reword could break.
type Category string

// The refusal categories, in triage order: the two that are about the WORKLIST, then the
// ones about the records a proposal names, then the ones about the values it states.
const (
	// CatStaleProposal: the worklist asks for something the fresh audit no longer
	// proposes, or no longer proposes the same way (including "it is advisory now").
	CatStaleProposal Category = "stale-proposal"
	// CatNotProposed: a key named in --only that selected nothing.
	CatNotProposed Category = "not-proposed"
	// CatMissing: the record the proposal names is not in the tree.
	CatMissing Category = "record-missing"
	// CatRetired: an earlier proposal in this run retired the record.
	CatRetired Category = "record-retired"
	// CatSidecarCollision: both halves of a duplicate carry the same works-community
	// member, so which CC BY-SA entry describes the survivor is a human decision.
	CatSidecarCollision Category = "sidecar-member-collision"
	// CatCommunityRequired: this tree does not hold works-community and no
	// --community root was given, so whether the cluster carries sidecars cannot be
	// answered - and CatSidecarCollision, the guard over the most expensive data in
	// the project, would be structurally blind rather than merely quiet. Every
	// merge-works proposal takes this until the flag is passed. It is the one
	// refusal that is about the RUN rather than about the records.
	CatCommunityRequired Category = "community-data-required"
	// CatPositionConflict: the change would put one book at two positions in a series,
	// or two books at one.
	CatPositionConflict Category = "series-position-conflict"
	// CatRecordingKey: a colliding recording key had no free numbered variant.
	CatRecordingKey Category = "recording-key-exhausted"
	// CatStaleValue: the record no longer states the value the proposal was written
	// against.
	CatStaleValue Category = "stale-value"
	// CatNoValue: the proposal states no value to write.
	CatNoValue Category = "no-value-stated"
	// CatMalformed: the proposal is not one this pass can read (no target, a field it
	// may not write, a position the data model rejects).
	CatMalformed Category = "malformed-proposal"
	// CatRedirect: pkg/redirects refused the tombstone a merge would have recorded.
	CatRedirect Category = "redirect-refused"
)

// Categories returns every refusal category, in triage order, so a consumer building a
// triage view over REFUSED.ndjson reads this list rather than writing one of its own.
func Categories() []Category {
	return []Category{
		CatStaleProposal, CatNotProposed, CatCommunityRequired, CatMissing, CatRetired,
		CatSidecarCollision, CatPositionConflict, CatRecordingKey, CatStaleValue,
		CatNoValue, CatMalformed, CatRedirect,
	}
}

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
	// Profile is which families DataDir holds (pack.Profile). The zero value is
	// pack.ProfileAll, so every existing caller is unchanged; `core` is what this
	// repository's root is since the community-repo split, and it is what keeps
	// the store, the post-write format pass and the post-write validation all
	// describing the SAME tree. A repair that healed under one profile and was
	// then judged under another would report a file it had just been told not to
	// touch.
	Profile pack.Profile
	// CommunityDir is the community checkout's data/ (KodeStar/audiosilo-meta-community),
	// opened READ-ONLY so the sidecar-collision refusal can see the layer this
	// repository no longer holds. Empty is the default and, under a profile that
	// disclaims works-community, makes every merge-works proposal refuse with
	// CatCommunityRequired - see sidecarSource. It is never written: moving a
	// sidecar member is that repository's own change, and the slug tombstone plus
	// the compose-time re-key already carry a member to the surviving work.
	CommunityDir string
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
	Category Category `json:"category"`
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
	category Category
	reason   string
}

func (r *refusal) Error() string { return string(r.category) + ": " + r.reason }

func refusef(category Category, format string, a ...any) error {
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

	store, err := pack.OpenForProfile(opts.DataDir, opts.Profile, writeFamiliesIn(opts.Profile)...)
	if err != nil {
		return nil, err
	}
	communityRO, err := openCommunity(opts)
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

	rn := &runner{opts: opts, plan: newPlan(store, communityRO, res.Catalog, table), rep: rep, filter: f}
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
	rep.Redirects = rn.plan.tombs

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
		err = refusef(CatMalformed, "op %q has no planner", p.Op)
	}
	if err == nil {
		if cerr := t.commit(c.key()); cerr != nil {
			// The only thing commit judges is the tombstone table, and it judges it
			// over a copy - so a refusal here has changed nothing.
			err = refusef(CatRedirect, "%v", cerr)
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
func (rn *runner) refuse(c candidate, category Category, reason string) {
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
	if rn.plan.tombs > 0 {
		if err := redirects.Write(rn.opts.DataDir, rn.plan.redirects); err != nil {
			return err
		}
	}
	// metafmt's own pass, not a restatement of it: canonical form plus the
	// self-healing placement work a deletion can leave due (a rebind of the lowest
	// pack, a split).
	fr, err := format.WriteProfile(rn.opts.DataDir, rn.opts.Profile)
	if err != nil {
		return err
	}
	rn.rep.Formatted, rn.rep.HealedWrote, rn.rep.HealedGone = fr.Formatted, fr.Wrote, fr.Deleted
	if fr.NeedsHuman() {
		return fmt.Errorf("repair: the tree needs a human after the write: %s (%s)", fr.Summary(), fr.Advice())
	}
	res := check.LoadProfile(rn.opts.DataDir, rn.opts.Profile)
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

// joinList renders a string list for a note or a message.
func joinList(ss []string) string { return strings.Join(ss, ", ") }
