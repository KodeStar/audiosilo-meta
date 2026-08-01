# PR notes: serving at scale (feat/serve-at-scale)

Working notes for the review of this branch. Not a doc: the durable parts are in
CLAUDE.md and README.md; this file records what the adversarial review confirmed
and what it deliberately left for other repos.

## What three reviewers attacked and could not break

Every claim below was produced by an EXECUTED reproduction, not by reading.

**Artifact verification and hot-swap (10 attacks, all refuted).** Corrupt
downloads in every shape - a clean-EOF short body, a lying `Content-Length`, a
single flipped byte in the gz, a valid gz carrying different bytes, a mangled
zstd delta - were all rejected before anything was installed. In each case the
live snapshot never moved, `loaded` never advanced, the target artifact path was
never created, and no temp file was left behind. A corrupt patch fell back to the
full download and succeeded. A poll-only boot took the full path even when the
release advertised a delta. Eight concurrent refreshes against one release
produced exactly one download and a coherent snapshot. The prune keep-set behaved
as documented: a foreign file in the cache directory survived, a leftover
`.meta-*.tmp` was collected, the `--db` artifact was left alone, and the patch
base survived its own adopt.

**Envelope parity, old and new (zero diffs).** The batched/windowed rewrite was
compared field-by-field against the pre-change implementation across all 1,070
series, 800 works, 800 people and 659 search queries, plus artifacts built at
older schema versions. No envelope differed.

**The D3 boot story, in a real container.** An image with no baked data was built
and run: it fetched the newest data release at boot, became ready, and served.
`/healthz` behaved as a readiness check throughout.

## What the review DID find (fixed on this branch)

Poller: the cache prune only spared the immediately-superseded snapshot, so a
third adopt inside one swap grace unlinked an artifact an older, still-draining
snapshot was serving from (15/16 concurrent queries failed with "unable to open
database file"); the single 60s `http.Client.Timeout` was a whole-request
deadline that, post-D3, gated readiness - a healthy but slow artifact download
aborted every attempt, forever; a restart re-downloaded an artifact the cache
volume already held, and a boot that could not reach GitHub answered 503 with a
loadable artifact sitting on that same volume; `Retry-After` always advertised
30s even after the loop had backed off to the interval. Workflow and docs:
`release.yml` did not trigger on builder changes, so a new index or a
`SchemaVersion` bump would not reach a release until an unrelated data edit; the
README described the degraded boot as "retries every 30s"; three descriptions of
the coverage `q=` parameter still described the LIKE substring behaviour the FTS
swap replaced; the site's import diff assumed `getPerson` returned a complete
authored list, which the new 500-row clamp makes false at seed scale; the person
page's heading and pagination note stated different units.

## Cross-repo follow-ups - NOT this branch

**audiosilo-docs.** Nothing in this repo's docs is stale for these, but the docs
site is:

- `/healthz` is now a READINESS check, not liveness: it answers 503
  `{"status":"starting"}` until an artifact loads. Deployment guidance should use
  it as a readiness/startup probe and NOT as a liveness probe, which would
  restart-loop a server that is patiently waiting out a GitHub outage.
- Any claim that the server "never starts empty" is no longer true by design (it
  is the D3 trade: the image ships no data).
- The overview still mentions baked data in the image; the image now builds the
  site and the binary only, and the catalogue arrives from the newest data
  release at boot.
- The new `?limit=`/`?offset=` parameters on `people/{id}` and `series/{id}`, and
  the `*_total` fields alongside them, are undocumented there.

**The site-out-of-image half of D3.** The image still bakes the static site, so a
site-only change still rebuilds and redeploys the image. Serving the site from
the same release/CDN mechanism as the data is future work; nothing on this branch
depends on it.
