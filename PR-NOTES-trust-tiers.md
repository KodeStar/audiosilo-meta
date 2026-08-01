# Trust tiers and the user-overwrite rule - PR notes

Notes for the pull request body. The policy itself lives in
[LICENSING.md](LICENSING.md#trust-tiers-and-the-user-overwrite-rule) and
[GOVERNANCE.md](GOVERNANCE.md); this file is what a reviewer needs in front of
them while deciding whether that policy is the one we want.

## The policy, in plain language

A record belongs to the mirror **only while every one of its `sources[]`
entries is `libex-import`**. That is the whole tier test - one entry of any
other type, from anyone, and the record has left the mirror tier for good.

While a record is the mirror's:

- A **user-tier import that matches it by exact ASIN takes over the facts its
  export states**, field by field: publisher, release date, runtime, cover,
  chapters, and ISBNs by append. Absence never deletes - a field the export
  does not state, or states empty, keeps the mirror's value. Every field is
  guarded that way; silence is not an assertion.
- A **user-tier import that merges a regional re-release ASIN** into the record
  does the same. That match is inferred (same work, same narrator set, a
  compatible runtime) rather than by identifier, and the merge stamps the user's
  provenance regardless - so the facts have to be applied in the same act or
  they are lost for good.
- The run's **source entry is appended, and that stamp one-way ends mirror
  status.** There is no "highest tier wins again" step, because that is exactly
  the last-writer-wins churn the policy exists to prevent.

After that:

- **Later create-path runs are skips.** A skip stays a skip: a second user's
  import does not backfill an attested record even where a fact is absent from
  it. Only `metaimport ... --enrich` fills what is absent.
- **A contradicting row is refused whole and writes nothing** - no provenance
  stamp - so a record that was a mirror seed stays attestable by the next
  (agreeing) import. First writer wins, flagged for review, never frozen.
- **Identical reruns are byte no-ops.** (One documented exception, benign: a
  merge attests only the recording, so the *work* is stamped by the next run's
  exact-ASIN skip. The run after that writes nothing at all.)

Never touched in any tier: identity (title, authors, narrators, the ASIN/ISBN
sets), `abridged`, `added_at`, person records, series membership. Those are
corrections, not imports.

## Policy edges to sign off on explicitly

These are choices, not bugs. Each is defensible; each could reasonably be
decided the other way, so they should be waved through deliberately rather than
inherited from the implementation.

1. **Attesting a recording also stamps its work.** No user-library export states
   a *work* fact today (no genres, and title/authors are identity), so the work
   is attested by a write that changes no field - the source entry is the whole
   attestation. Consequence: the work leaves the mirror tier on a zero-field
   write, and a later, better user import can no longer overwrite it. The
   alternative is to leave the work in the mirror tier until something actually
   states a work fact, at the cost of the recording and its work sitting in
   different tiers.
2. **User-vs-user disagreements beyond runtime and release date resolve
   first-writer-wins silently.** Only two fields are checked for contradiction:
   a runtime more than 10% apart, and a release date that is not the same date
   at another precision. A differing publisher spelling, cover URL or chapter
   table is not reported at all - the recorded value simply stands. That is
   deliberate (publisher spellings differ across marketplaces and imprints, and
   flagging them would drown the real disagreements), but it means "where two
   users disagree the disagreement is surfaced" is true only for those two
   fields. The docs now say so.
3. **Retraction integrity rests on dangling-reference detection, not on
   stamping.** People are reused by slug and never stamped by the imports that
   credit them, so a mirror-seeded person can still read as bulk-mirror-only
   while attested works credit it. A whole-record retraction of the mirror must
   therefore keep those people; what catches a mistake is `metacheck` (an
   unresolvable credit is a problem, not a warning), not the tier test. Closing
   that properly would mean stamping reused records or per-field provenance -
   both bigger changes than this PR.

## Correction to the simplify commit's message

The `refactor(importer): drop the inert equality layer` commit claims "the
artifact bytes a run writes are unchanged". That is wrong in one small, benign
way, and the record should say so.

`fillStr`'s removed early return compared `coerceStr(raw[key])` - which
**trims** - against the row's value. So an on-disk value that differed from the
row's only by surrounding whitespace compared equal, and the old code left those
untrimmed bytes in place. Without that layer, the overwrite posture now rewrites
the field with the trimmed value.

It is a normalization, not an idempotency break: it only applies in the
overwrite posture (in the ordinary posture a recorded value blocks the write
anyway), the value it writes is the same string canonical formatting would
prefer, and the run's own attestation flips the tier so the next run does not
touch the field at all. It also cannot arise from an import - only from a hand
edit that left whitespace in a record the mirror alone had sourced.
