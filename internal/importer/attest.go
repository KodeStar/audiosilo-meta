package importer

import (
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// attest.go is the importer's half of the TRUST-TIER / user-overwrite policy
// (LICENSING.md, "Trust tiers and the user-overwrite rule"; the tier model
// itself is pkg/model/trust.go).
//
// The problem it solves: the catalogue is about to hold ~135k records nobody
// has attested, seeded from the libex mirror. When a real person's library
// export finally names one of those books, their data must be able to WIN over
// the seed - otherwise the mirror is the permanent provenance and the "real
// users become the provenance over time" promise never happens.
//
// The rule has exactly two halves, and both must hold:
//
//	run-wide   the run's source type is user-library tier (planner.userTier)
//	per-record the record the row matched is still bulk-mirror-only
//
// When both hold the row's stated facts overwrite the recorded ones and the
// run's source entry is appended - the record is now user-attested and, by
// construction, no longer bulk-mirror-only. Every later run (including another
// user's) is back to the ordinary posture: existing wins, fill only what is
// absent, contradiction guards refuse the row. First writer wins; a
// disagreement is warned about, never applied. There is deliberately no
// "highest tier wins again" step, because that is exactly the last-writer-wins
// churn the policy exists to prevent.
//
// What is NOT overwritten, in any tier:
//
//   - added_at, on either a work or a recording. It stamps when the record
//     entered the database, which is not a fact an export states. Nothing in
//     this file writes it; the only branches that ever do are the ones that
//     CREATE a record.
//   - identity: a work's title/authors, a recording's narrators and language,
//     and the asin[]/isbn[] identifier sets (ASINs are only ever added, ISBNs
//     are globally unique and only ever appended). Changing one of those is a
//     correction - the correct-data form - not an import.
//   - abridged, whose absence the on-disk model cannot tell from a stated
//     false, so a write here could invent an edition fact.
//   - person records. A credit resolves by slug, and the row does not restate
//     the person, only names them; nothing here would have anything to write.
//   - series membership and position. A series entry is shared by many works
//     and the row only claims one membership; re-positioning a catalogued work
//     is a correction, not an attestation.

// attestExisting is the create/recordings-only path's tier hook, called at the
// ASIN-DEDUP skip: the row names a book the catalogue already holds, which is
// precisely where a user's own library meets a record the mirror seeded.
//
// It is a no-op for anything but a user-library run, so a libex import's skip
// behaves exactly as it always has.
//
// A row whose ASIN is known but has no location was claimed by THIS run (the
// planner registers created ASINs in p.asins, but p.asinLoc is seeded only from
// what was on disk), so the record it would attest is one this run just wrote
// with its own provenance - nothing to do.
func (p *planner) attestExisting(b sourceBook, asin string) {
	if !p.userTier {
		return
	}
	ref, located := p.asinLoc[asin]
	if !located {
		return
	}
	warn := p.bookWarn(b)
	// The recording and the work it belongs to, at the granularity the
	// ASIN-matched flows already work at. The series claim is deliberately not
	// applied here: placing a work into a series is enrichment's job, and this
	// is an exact-ASIN attestation of one edition, not a re-import of the book.
	if p.applyToRecording(b, ref, warn, scopeAttestExact) {
		p.applyToWork(b, ref, scopeAttestExact)
	}
}

// Attestation is ONE hand submission - an intake issue form - stated as the
// facts the takeover reads. It is the form's counterpart of an export row, and
// deliberately carries only what an export row can state about an edition the
// catalogue already holds: identity (title, authors, narrators, the identifier
// sets) is never rewritten by an attestation, so it has no field here.
type Attestation struct {
	// ASIN is the catalogued ASIN the submission names. It IS the match:
	// LICENSING.md's rule takes over a mirror seed a user names BY ASIN, so
	// there is no second way to say which record this is.
	ASIN string
	// Source is the provenance entry the takeover stamps: the submission's own
	// entry, which must be user-library tier (a form stamps `user`). A source
	// with no overwrite authority is refused rather than run as an enrichment.
	Source OutSource
	// The stated facts, each empty or zero when the submission did not state it -
	// silence is not an assertion, so an unstated fact keeps the mirror's value.
	RuntimeMin  int
	ReleaseDate string
	Publisher   string
	CoverURL    string
	ISBNs       []string
	// Genres are values of the project's own vocabulary (the form validates them
	// against the schema enum), unioned into the work's set - LICENSING.md rule 5.
	Genres []string
}

// AttestAt applies the user-overwrite rule (LICENSING.md, "Trust tiers and the
// user-overwrite rule") for ONE hand submission naming, by ASIN, a book the
// catalogue holds, and writes and validates the tree exactly as a run does. It
// is the intake bot's door onto the rule; the library import's door is the
// create path itself.
//
// It is not a second implementation of the rule: it plans the submission as a
// one-row user-library run and hands it to the same hook the create path calls
// at its ASIN-dedup skip (attestExisting), so the two doors cannot disagree about
// what a takeover is. Against a bulk-mirror-only record the stated facts replace
// the recorded ones (an ISBN is appended when no recording claims it) and Source
// is appended (Summary.Attested*); against an attested record nothing is
// written, first writer wins. Either way a runtime more than 10% apart, or a
// release date that is not the same date at another precision, refuses the row
// whole and is counted in Summary.Conflicts, with the warning naming both values
// - the disagreement a maintainer adjudicates.
//
// The caller holds the tree's store already and has matched the ASIN itself:
// ref is the recording a.ASIN names, and taken lists those of a.ISBNs the
// catalogue records ANYWHERE. a's text is taken as ALREADY DECODED - the form
// door decodes a field once as it reads it (issueform's sections.get), so a run's
// decodeText here would decode a submitted "&amp;amp;" twice. It loads no
// catalogue, which is sound because attestExisting's call graph reads exactly
// three pieces of planner state beyond the store and the run's own fields, and
// this seeds each of them from what the caller already knows:
//
//   - asinLoc, for the one ASIN: where the record it attests sits;
//   - isbns, for the submitted ISBNs already taken: the global-uniqueness set
//     claimISBNs refuses a duplicate identifier against (claimISBNsFor drops the
//     record's OWN ISBNs first, so seeding one of those changes nothing);
//   - works, left EMPTY: applyToWork reads a work's run state (runGenresOwned,
//     runAttested), which is zero for every record on disk, and builds that zero
//     state itself when the map has none.
//
// Everything else a whole-catalogue load fills (people, series, the identity
// index, the credit censuses) serves the create path, which an attestation never
// reaches. TestAttestAtMatchesAWholeLoad pins the equivalence.
//
// An ASIN the catalogue does not hold is the caller's error: this entry point
// takes over records, it never mints them.
func AttestAt(store *pack.Store, ref RecRef, taken []string, a Attestation, opts Options) (Summary, error) {
	asin, err := a.check()
	if err != nil {
		return Summary{}, err
	}
	p := plannerOn(store, a.Source.Type, opts)
	p.asinLoc[asin] = ref
	for _, isbn := range taken {
		p.isbns[strings.ToUpper(isbn)] = true
	}
	return p.attest(a, asin, opts)
}

// check refuses an attestation no run could apply, returning its normalized ASIN.
func (a Attestation) check() (string, error) {
	if model.TierOfSource(a.Source.Type) != model.TierUserLibrary {
		return "", fmt.Errorf("attest: source type %q has no overwrite authority", a.Source.Type)
	}
	asin := NormalizeASIN(a.ASIN)
	if asin == "" {
		return "", fmt.Errorf("attest: %q is not an ASIN", a.ASIN)
	}
	return asin, nil
}

// attest is the attestation's body over a seeded planner: the submission as a
// row (its text already decoded - see AttestAt), the create path's attestation
// hook, and the run's write tail.
func (p *planner) attest(a Attestation, asin string, opts Options) (Summary, error) {
	b := sourceBook{
		raw: rawBook{
			"asin": asin, "release_date": a.ReleaseDate,
			"publisher": a.Publisher, "image_url": a.CoverURL,
		},
		runtimeMin:  a.RuntimeMin,
		isbns:       a.ISBNs,
		vocabGenres: a.Genres,
	}
	// The submission is a row door of its own (estimate.go).
	p.setEvidenceRuntime(&b)
	// The submission's own entry, not setSource's: a run stamps the row's ASIN as
	// its ref, where a hand submission's provenance is what the submitter cited.
	p.curSource = a.Source
	p.curStatesRuntime = b.runtimeMin > 0
	p.attestExisting(b, asin)
	if p.fatal != nil {
		return p.result(), p.fatal
	}
	// A refused or no-op attestation queued nothing, so there is no tree to write
	// or re-validate.
	if p.summary.Produced() == 0 {
		return p.result(), nil
	}
	err := p.commit(opts)
	return p.result(), err
}

// attestOnMerge is the ASIN-MERGE path's tier hook: a user's row is about to
// fold a re-release ASIN into a sibling recording, which stamps the run's
// source on that record and so ENDS its bulk-mirror-only status. If the record
// is a mirror seed, this is the only moment its facts can still be overwritten,
// so the attestation happens here rather than being lost.
//
// On a record it may not overwrite it writes nothing and says nothing
// (scopeAttestMerged): an ASIN merge has always been deliberately narrow - the
// ASIN, the provenance stamp, any unclaimed ISBN, see addBook - and the row is a
// different edition, so backfilling from it (or flagging its differing regional
// facts) would be reading more into an inferred match than it carries.
//
// Only the recording is attested, never the work: the work was resolved by title
// and author set, not by identifier, and the merged edition says nothing about
// the work that the recording does not.
func (p *planner) attestOnMerge(b sourceBook, ref RecRef, warn func(string, ...any)) {
	if !p.userTier {
		return
	}
	p.applyToRecording(b, ref, warn, scopeAttestMerged)
}

// overwrites is the per-record half of the trust-tier decision, and the ONLY
// place a writer asks it: does this run get to overwrite what raw already
// records? Both halves must hold - a user-library run, and a record whose every
// provenance entry is bulk-mirror tier.
func (p *planner) overwrites(raw map[string]any) bool {
	return p.userTier && model.BulkMirrorOnly(rawSources(raw))
}

// rawSources reads a decoded record's sources[] array (type and imported_at,
// the two facts the trust tier and the preorder-estimate rule read). A
// malformed entry yields an empty type, which pkg/model ranks as reference tier
// - so a record we cannot fully read is never classified as overwritable.
func rawSources(raw map[string]any) []model.Source {
	arr, _ := raw["sources"].([]any)
	out := make([]model.Source, 0, len(arr))
	for _, s := range arr {
		m, _ := s.(map[string]any)
		out = append(out, model.Source{Type: coerceStr(m["type"]), ImportedAt: coerceStr(m["imported_at"])})
	}
	return out
}
