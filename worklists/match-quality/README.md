# Match-quality worklist (LibraForge benchmark follow-up)

What the `data/match-quality` branch found and did NOT apply. Every row here is a
candidate for a human, not a decision: nothing in this folder is read by the tooling.
Delete a row (or the file) once it is decided, and record the decision in
`internal/audit/reviewed.json` as usual (`assert` to merge, `reject` to keep apart).

## series-designation-pairs.ndjson

The `series-designation-duplicate-works` advisory (`metacheck`, pkg/check/seriesdesignation.go)
after this branch merged 262 pairs: one book filed twice by the same authors, the
second title wrapped in a series designation ("Heartfire: The Tales of Alvin Maker,
Book 5"), a listed series name, the author's possessive, a genre tag, or another
spelling of the same title. One row per pair:

- `plain`, `decorated` - the two work ids; `plain_title`, `decorated_title`.
- `kind`, `designation` - what the check read as the designation.
- `languages`, `shared_narrator`, `runtime_diff` - the evidence the merge wave was
  chosen by (`runtime_diff` is the closest recording pair's relative difference,
  0.02 = 2%): `evidence` is `high` (same language, a shared narrator, runtimes
  within about 4%), `mid` (a shared narrator, runtimes further apart or unstated)
  or `low` (no shared narrator); `unknown` is a pair that only surfaced once the
  wave's merges joined two works.
- `status`:
  - `held: the decorated twin carries community sidecars` - merging is right on the
    evidence, but the twin's characters/recaps must be re-keyed in
    audiosilo-meta-community in the same change.
  - `held: an audit veto disagrees` - a metaaudit veto argues against the merge.
  - `held: an existing reviewed decision names one side` - read that decision first.
  - `open: needs a human look` - weaker evidence; many are real twins, some are two
    editions or a boxed set.

Rerun: `go run ./cmd/metacheck --profile core data 2>&1 | grep "series designation"`.

## narrations.ndjson

`metaimport narrations` (internal/importer/narrationcheck.go): a recording whose
ASINs libex credits to two different narrations (`asins-name-different-narrators`),
or whose every looked-up ASIN credits other narrators than the recording
(`recording-credits-other-narrators`). This run covered the recordings with two or
more ASINs in one marketplace that came from a user library export
(`--user-library`), plus the benchmark's works.

The run: 973 recordings, 3,850 ASIN lookups, 23 findings, plus the benchmark's
Catch-22 and The Hobbit. Applied on this branch (`status: applied`): The Martian
(`r-c-bray-2020` held Wil Wheaton's 2020 production; split into `wil-wheaton-2020`)
and six works whose newer production already had its own recording (the ASIN, the
sources naming it and the chapters fitting it moved there). Two findings were the
check misreading a spelling and are fixed in the check (`not a finding`).

The pattern behind nearly every real finding: a user-library import
(`audiosilo-books-import` + `audible-lookup`, both naming the foreign ASIN) matched a
newer production (William Hope's 2023 Foundation, the Penguin Discworld cast
recordings) onto the older recording by title and author. The stored chapters
usually fit the foreign production; the runtime and cover may be either's. The
`open: split ... into a new recording` rows need a new recording (and often a new
narrator person) built from the foreign ASIN's libex row, with the chapters moved
when the status says they fit it.

Rerun: `go run ./cmd/metaimport narrations --user-library -o narrations.ndjson`
(read-only; add `--works id,id` for named works, drop `--user-library` for all
8,872 multi-ASIN recordings, which is a long, polite run against libex).
