# Pack-file storage spec (2026-07-30)

Design spec for migrating audiosilo-meta's data tree from file-per-entity to
**range-packed files** - many entities per JSON file, adaptively split by slug
range. Companion to [SCALE-RESEARCH.md](SCALE-RESEARCH.md) (the measurements and
the decision rationale, incl. the addendum). Status: **IMPLEMENTED** (`pkg/pack`,
with `cmd/metamigrate` having performed the one-off conversion) - signed off by
Chris 2026-07-31 after the validation spike
(see "Validation spike results" below + [PACK-SPIKE-RESULTS.md](PACK-SPIKE-RESULTS.md));
the open questions are resolved in "Decisions" at the end. References to the
"current" per-record files below describe the pre-migration layout this spec
converted. Numbers below come from the real tree at 9,517 works.

## Goals

- One storage model that works unchanged at 10k, 150k, and 1M+ works (the
  "usable in 6 months" requirement).
- Keep the repo as the database: PR-reviewable diffs, human-editable files,
  git history as the audit trail.
- Keep the structural license boundary (CC0 core vs CC BY-SA sidecars).
- Keep slugs as identity and the SQLite artifact/API contract byte-compatible
  (modulo the added_at change below). serve, site, player: untouched.
- Preserve human browsability by name (supersedes the hash re-shard idea, which
  would have destroyed it).

Non-goals: changing entity schemas beyond the two additions noted (`added_at`,
pack wrapper); multi-repo layouts; changing the release artifact.

## Measured inputs that drive the sizing

| Entity | Count today | Avg canonical bytes | Notes |
|---|---|---|---|
| work + its recordings (composite) | 9,517 | 3,638B (median 2,399B, p95 10.8KB, max 61.5KB) | recordings avg 3,774B (7,853 of 8,101 with chapters), works 425B |
| sidecar pair (characters + recaps) | 638 works | ~18KB (chars 8KB + recaps 10KB) | rare but large entries |
| person | 4,016 | 212B | |
| series | 1,070 | 904B | |
| whole tree | 23,980 files | **47.9MB actual JSON** | 117MB on disk = ~2.4x block inflation |

*(Re-measured 2026-07-30 after the recording chapter backfill from libex
`tracks` merged - PR #1204, 7,027 recordings backfilled. The original
pre-backfill composite average was 1,351B.)*

## Layout

```
data/works/<dir-bound>/<pack-bound>.json           CC0      work composites, keyed by work slug
data/works-community/<dir-bound>/<pack-bound>.json CC BY-SA characters+recaps, keyed by work slug
data/people/<pack-bound>.json               CC0     keyed by person slug
data/series/<pack-bound>.json               CC0     keyed by series slug
```

Four **pack families**. Works and sidecars get a directory level from day one
(they are the high-count families); people and series start flat and gain a
directory level only if they ever exceed the per-dir pack cap.

**One non-pack file** sits beside them, at the data root (added 2026-08-14):
`data/redirects.json`, the slug tombstone table (`{"works": {"<retired>":
"<live>"}, "people": ..., "series": ...}`) that keeps a slug a merge retired
resolving. It is deliberately not a family - a retired slug is no entity, so
there is nothing to place, bound, split or heal - and the accounting is where the
layout has to know about it: `pack.RedirectsFile`/`Listing.Aux` classify it as
recognized, so the invariant that every JSON file under the data root is
accounted for (see below) stays total instead of gaining an exemption at each
caller. Nothing in `pkg/pack` reads or writes it; `pkg/redirects` owns the file
and `pkg/check` validates it. The pack merge driver is kept off it by a
`.gitattributes` line, since a union of pack ENTRIES is not what a conflict there
needs - and the driver itself refuses any file with no top-level `entries` object,
because reading a missing one as empty turned that file into `{"entries":{}}` and
reported success.

Sidecars move out of `data/works/**` into their own family - named
**`works-community`** (decided at sign-off: the name telegraphs the CC BY-SA
community layer) - so the license
boundary becomes directory-structural as well as schema-structural: a works pack
schema-locks every entry to `CC0-1.0`; a sidecar pack schema-locks to
`CC-BY-SA-3.0` (`$defs/license_content`). Mixing is impossible by construction,
as today.

## Pack file format

A pack is a single canonical-JSON file:

```json
{
  "entries": {
    "<slug>": { ...entity... },
    ...
  }
}
```

- **Works family entry** = the composite: all current `work.json` fields plus
  `"recordings": { "<rec-slug>": { ...recording fields... } }`. Recordings keep
  their own `license`/`sources` exactly as today; the rec-slug map key replaces
  the recording filename. Rationale for nesting rather than a separate
  recordings family: one write path per book, and the composite mirrors the
  shape metabuild and `GET /works/{id}` already assemble.
- **Sidecars family entry** (keyed by work slug) =
  `{ "characters": {...current characters.json...}, "recaps": {...current recaps.json...} }`,
  either member optional.
- **People / series entries** = the current file contents, unchanged.
- The top-level wrapper holds only `entries` for now
  (`additionalProperties: false`); pack-level metadata can be added by schema
  revision later.
- Canonical form is unchanged pkg/canonical (sorted keys, 2-space, trailing LF).
  Sorted keys give slug-sorted entries **for free** - entry order is enforced by
  the existing formatter, no new ordering rule needed.

## Bounds and naming (the B-tree rules)

- A pack file's name (minus `.json`) is its **lower bound** - a valid slug
  string. A pack covers slugs in `[own bound, next sibling's bound)`, siblings
  sorted lexicographically. The **very first pack/dir of a family** is named
  `0.json`/`0/` (bound `"0"`, the minimum possible slug prefix - `0` sorts at
  or before every valid slug, and the bound is inclusive so a literal slug `0`
  still lands correctly).
- The directory level uses the same rule: `data/works/<dir-bound>/` covers
  packs whose bounds fall in `[dir bound, next dir's bound)`. **A directory's
  bound is its first pack's bound** - so `data/works/0/0.json` exists in the
  first dir, and a later dir looks like
  `data/works/harry-potter/harry-potter.json`. (The spike caught that naming
  every dir's first pack `0.json` would contradict the pack-in-dir-range
  invariant; this is the corrected rule.)
- **Bounds change only on split.** Inserts and deletes never rename a pack.
  Lookup is a binary search over sorted names - no index file, the tree IS the
  index.
- **Split rule:** when a pack exceeds a hard cap, split at the median byte
  position's entry boundary; the upper half becomes a new pack named by its
  first slug. When a directory exceeds its pack-count cap, split it the same
  way. Splits are performed by `metafmt --write` and must be deterministic
  given the tree state (same overfull pack ⇒ same split point).
- **No automatic merges** (avoids churn/hysteresis). Merging adjacent
  underfull packs is a manual maintenance operation, expected to be rare
  (deletions are rare).
- Packing is therefore **history-dependent** (like any B-tree): the same
  logical data seeded in a different order can produce different pack
  boundaries. That is fine - metacheck validates the invariants (below), not a
  canonical packing. Determinism of the *artifact* is unaffected: metabuild
  sorts by id, not by pack.

## Caps (final - confirmed/adjusted by the validation spike, signed off)

| | target size | hard size cap | entry cap | dir pack cap |
|---|---|---|---|---|
| works | 256KB | 512KB | 1,000 | 512 packs/dir |
| works-community | 256KB | 512KB | 200 | 512 |
| people | 256KB | 512KB | 1,000 | 512 |
| series | 256KB | 512KB | 1,000 | 512 |

Rationale: 512KB keeps GitHub's blob view and PR diff rendering comfortable
(measured ~4x headroom); 512 packs/dir stays under GitHub's 1,000-entry
directory render. Size caps are measured on the pack's **rendered canonical
bytes** (what the file holds after metafmt), not its current on-disk bytes -
the cap governs the file's steady-state form, and metafmt normalizes before
judging (implementation pin, 2026-07-31). The people entry cap was lowered from 2,000 at sign-off: at
the measured 268B/person, size binds at ~954 entries, so 2,000 could never
fire. Initial migration fills packs to ~50% of target so organic inserts have
headroom before the first splits.

**Single-entry exemption (sign-off addition):** a pack holding exactly one
entry is exempt from the hard size cap - an entry larger than the cap cannot
be split, so without the exemption metacheck would fail on unfixable data (the
largest measured work composite is 236,482B, only 2.2x under the cap).
metacheck instead emits an advisory warning for any single entry over 256KB so
outliers get looked at.

**Sizing re-measured (2026-07-30, post-backfill):** the chapter backfill
landed and the composite average is now **3,638B** (measured, chapter-inclusive
- within the predicted 2.5-4.5KB band). That gives ~70 avg works per 256KB
pack at target fill, ~35 at the 50% initial migration fill.

Projected file counts (at the measured 3.6KB composite avg): today ≈ **~190
packs** at target fill (from 23,980 files). At 152k works (after the full
142,552 seed) ≈ ~2,200 packs at target fill, ~4,300 at the 50% initial fill.
At 1M works ≈ ~14-15k packs across ~30 dirs. `git status`, clones, CI
checkout, and block overhead all become non-issues at every scale.

## Invariants (new metacheck rules, each with pass/fail fixtures per convention)

1. **Placement**: every entry's slug lies within its pack's `[bound, next)`
   range, and every pack lies within its directory's range.
2. **Caps**: no pack exceeds its hard size/entry cap; no dir exceeds its pack
   cap.
3. **Bound validity**: file/dir names are valid slugs (or the reserved `0`);
   sibling bounds strictly increasing; no empty packs.
4. **License lock**: per-family license enum enforced by the pack schemas
   (works/people/series ⇒ `license`, sidecars ⇒ `license_content`).
5. All existing cross-entity rules (integrity, ASIN/ISBN/wikidata uniqueness,
   series positions, genres sorting, character/recap rules) are unchanged -
   they run on the parsed catalogue and never cared about file layout.

**Self-healing placement:** `metafmt --write` relocates misplaced entries to
their bound-correct pack and performs any due splits; `metafmt --check` (CI)
fails with the canonical location named. A contributor can add an entry to
approximately the right file - or the wrong one - and formatting fixes it.
This is the contributor-ergonomics answer: nobody has to compute placement by
hand, ever.

A plain writer (an importer, the intake bot) does NOT heal: it refuses. A pack
holding an entry that belongs in a later pack has a range that no longer
describes what it holds, and splitting it would mint a bound that later pack
already carries - so `Store.Flush` names the entry and stops, before it has
written anything at all. Healing is `metafmt --write`'s job, and its resolution
rules (the correctly-placed copy wins) are the ones that must decide.

The refusal covers the packs a flush READS, which is every pack it could
rewrite: an untouched, in-cap pack is never opened, mints no bound and so
cannot collide with anything. A misplacement sitting in one of those is
invisible to the writer and stays the healer's business - `metafmt --check`
reads every file under a family root and reports it, which is why CI runs it.

## added_at becomes an explicit field

Pack files break the `git log --diff-filter=A` derivation (a pack's add-date is
not its entries' add-dates). Replace it:

- New optional `added_at` (date) field on **works and recordings** (sign-off
  decision: both, while we're adding it - the artifact only exposes works'
  added_at today, recordings' is future-proofing), stamped at creation by the
  importer and the intake bot.
- The migration backfills it from git history **one final time** (the current
  release.yml awk walk, run once, output written into the entries).
- metabuild reads the field; the `--added` flag, the release.yml git-walk step,
  and its `fetch-depth: 0` requirement are retired. Release checkouts become
  shallow; git history stops being load-bearing entirely (which also unlocks a
  future history squash if the repo ever wants one).
- The existing `sources[].imported_at` lexicographic fallback stays (normalize
  timezones while touching it - the latent bug noted in SCALE-RESEARCH.md C4).

## Blast radius (what the migration touches)

- `pkg/model`: `Location` becomes (pack path, slug[, rec-slug]); `Shard()`
  retired; `ParseLocation` reworked (landed as: deleted, not reworked - the
  callers that still read a per-record path parse it themselves, as a reference
  syntax); `MaxSlugLen` unchanged.
- `pkg/check`: tree walker iterates packs; `checkStructure` replaced by the
  placement/caps/bounds rules; all rule logic on the parsed catalogue unchanged.
- New `pkg/pack` (or inside pkg/canonical): bound math, lookup, split,
  relocate - the one shared implementation every writer uses.
- `pkg/canonical`: unchanged per-file; metafmt gains placement/split.
- `internal/importer`: write paths become read-modify-write per pack, batched -
  a nice side effect: the 142k seed touches ~1,600 packs instead of ~350k
  files, so the bulk `git add` drops from minutes to seconds.
- `internal/issueform`: compose paths write into packs via pkg/pack.
- `internal/build`: walker only; insert logic unchanged; artifact unchanged.
- `.github/workflows/release.yml`: drop added.tsv step + full-depth checkout.
- Docs: CLAUDE.md, CONTRIBUTING, AUTHORING, EXTRACTION*, GOVERNANCE examples.
- The site and serve layers: **no changes** (they consume the artifact/API).

## Trade-offs accepted

- **Per-entity git blame/log is lost** (`git log -- <file>` now shows the whole
  pack). Mitigation: `git log -S '"<slug>"'` pickaxe finds an entry's history;
  intake/import commit messages already name slugs/ASINs. If this bites, a
  small `metahistory <slug>` helper can wrap the pickaxe later.
- **Adjacent-insert merge conflicts** between two PRs targeting the same pack
  region. Sorted canonical form keeps hunks minimal; the intake bot serializes
  its own PRs and can rebase + re-run metafmt mechanically; human guidance is
  "rebase, run metafmt --write, re-push".
  Since 2026-08-09 the mechanical step is a git **merge driver**
  (`.gitattributes` -> `scripts/pack-union-merge.sh`) rather than a pass over the
  files git already merged: a pack is a map, and git's line merge can anchor two
  branches' insertions of the SAME entry key at different lines and apply both,
  leaving the key in the file twice - which no reader accepts and metafmt cannot
  repair (issue #1695). The driver merges the entry maps before that can happen
  and refuses exactly what the by-hand recipe refuses.
- **Web-UI deep links** to a specific entity's file are gone (link to the pack
  + in-page search instead; the site/API remains the browse surface).
- **History-dependent packing** (see bounds section) - accepted, invariants
  checked instead.

## Migration plan (flag day, one logical change set)

1. **Tooling PR**: pkg/pack + all writers/readers/rules support the pack
   layout; fixtures migrated; gate green against a converted fixture tree.
2. **Migration PR**: script converts the live tree (fill to ~50% target in
   slug order), backfills `added_at` from git history, deletes the old tree.
   Big but mechanical; reviewed by artifact-diff (see 3).
3. **Equivalence proof**: metabuild before vs after must produce identical
   artifacts (same schema_version; added_at values identical since they were
   backfilled from the same history the old path read). This is the QA gate
   for the whole migration.
4. Cut a data release; confirm metaserve hot-swaps it and the site behaves.
5. **Then** the 142,552-work seed lands on the new layout, in wave PRs sized
   by pack count rather than file count.

## Validation spike (before committing to the format)

Generate the full 152k-work catalogue (current data + the 142k selection) in
pack layout in a scratch repo and measure:

- git: bulk add/commit, `git status`, clone, `.git` size after gc.
- Gate timings: metacheck/metafmt/metabuild wall + RSS (expect I/O win from
  ~1,600 reads vs 350k).
- Churn: simulate ~1,000 random intake-style single-entry edits + inserts;
  measure split frequency, conflict rate between concurrent branch pairs, and
  diff readability on a draft GitHub PR (render check at the 256/512KB caps).
- Confirm the cap table above or adjust it.

## Validation spike results (2026-07-30) - the spike has run

The spike described below has been executed at full scale; the complete report
with methodology, tables, and example diffs is
[PACK-SPIKE-RESULTS.md](PACK-SPIKE-RESULTS.md). Headline numbers (135,219 works
- the 142,550-row E1 selection, chapters included, run through the real
importer, merged onto the current tree; fewer than 152k because ASIN merges
collapse regional re-releases, which is correct):

| | file-per-entity (control) | packs |
|---|---|---|
| files | 355,012 | **6,654** |
| bulk `git add -A` | 334 s | **14.7 s** |
| `git commit` | 207 s | **0.74 s** |
| `git status` | 19 s | **0.02 s** |
| local `git clone` | 83 s | **4.2 s** |
| `.git` after gc | 173.5 MiB / 625,580 objects | **105.7 MiB / 6,687 objects** |
| `du` on disk | 1.6 GB | **811 MB** |

Gate at that scale (sequential, M1 Ultra): metacheck-equivalent 77.8 s /
**1.18 GB RSS** (7 GB CI runners unthreatened); metafmt-equivalent 16.3 s;
metabuild-equivalent 56.0 s, artifact 813 MB raw / 205 MB gz. Churn: **1 split
per 1,000 intake-style ops** even under a deliberately clustering insert
generator; single-field edits are one-line hunks. Conflicts: ~0.26% of
concurrent PR pairs (arithmetic from the measured pack count); when forced,
**46/46 conflicted merges resolved mechanically** by union + canonical
re-render - the metafmt self-healing recipe works. GitHub render (draft PR
left in place: https://github.com/KodeStar/pack-render-spike/pull/1): a
one-line edit is a ~160 B patch at 256 KB, 512 KB and 1 MB alike; whole-pack
additions collapse at every size; the blob view renders to at least 2.05 MB,
so 512 KB has ~4x headroom.

**Confirmed/adjusted cap table:** all 256 KB target / 512 KB hard / 512
packs-per-dir values confirmed, with two changes: people entry cap 2,000 ->
**1,000** (at 268 B/person, size binds at ~954 entries, so 2,000 could never
fire), and a **single-entry exemption to the hard size cap** (largest measured
work composite is 236,482 B - an entry bigger than the cap cannot be split, so
without the exemption metacheck would fail on unfixable data; pair it with an
advisory warning at >256 KB).

Findings that amend this spec when implementation starts:

1. **Directory naming**: "first pack in any family/dir is `0.json`" as written
   contradicts the pack-in-dir-range invariant for every directory after the
   first. Fix: a directory's bound is its first pack's bound; only the very
   first pack/dir of a family is named `0`.
2. **Raw JSON bytes go UP ~40%** in pack layout (600 MB -> 839 MB at 135k
   works): canonical indent-2 nests entities 2-4 levels deeper and chapter
   arrays are line-dense. Disk (block inflation 2.7x -> 1.03x) and git still
   win decisively; just don't be surprised by a bigger `data/` byte count.
3. **Importer defect independent of layout**: at 142k scale the
   title+author disambiguation mints 16 work slugs over `MaxSlugLen` (100),
   cascading into 65 validation failures. `getOrCreateWork` must
   truncate/rehash before the seed lands - fix it before or with the
   migration.
4. **Conflict recipe belongs in CONTRIBUTING** and in the intake bot: rebase,
   `metafmt --write` (union + canonical re-render), re-push - measured 100%
   mechanical.

Spike methodology caveat: the libex dump's `tracks` table is essentially empty
(448 of 1.13M rows), so chapters were synthesized from real live-repo chapter
arrays (runtime-matched donors, rescaled). Byte volumes are faithful; the
chapter facts are not real and nothing from the spike is committable data. The
real seed will need a chapter source (per-title libex lookups, as the backfill
used, or none at seed time).

## Decisions (2026-07-31 sign-off by Chris)

The five open questions, resolved. The spec body above has been updated to
match - this section is the record.

1. **Cap values: final.** 256KB target / 512KB hard / 512 packs-per-dir for
   all four families, with the spike's two adjustments: people entry cap
   2,000 -> 1,000, and the single-entry hard-cap exemption (+advisory warning
   over 256KB). Going larger buys nothing (edit diffs are size-independent;
   whole-pack adds collapse at every size).
2. **Series are packed** - 161 packs at 31k series in the spike, costs
   nothing, one code path.
3. **`added_at` goes on recordings too**, same one-time git-history backfill
   as works. The artifact keeps exposing only works' added_at for now.
4. **The intake bot pre-resolves placement** (computes the target pack in the
   PR); humans rely on `metafmt --write` self-healing. The bot also
   auto-rebases conflicted intake PRs with the measured-100%-mechanical
   recipe (rebase, union entries, canonical re-render, re-push); the same
   recipe goes in CONTRIBUTING for humans.
5. **The sidecars family is named `data/works-community/`** - early enough in
   the project that the rename is cheap, and the name telegraphs the CC BY-SA
   layer.

Plus the spike-surfaced amendments, accepted: the directory-bound naming fix
(a dir's bound = its first pack's bound; only a family's very first pack/dir
is `0`), and the importer slug-length fix (`getOrCreateWork` bounding
disambiguated slugs to `MaxSlugLen`) landing before or with the migration.
