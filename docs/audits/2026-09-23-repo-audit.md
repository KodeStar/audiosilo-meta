# Repository audit - 2026-09-23

A full audit of audiosilo-meta: security (Go and site dependencies, the API
server, the intake path), CI workflows, code quality, every issue form, a
functional smoke test of the built artifact, and repository size. Every finding
carries a recommended fix.

- **Baseline:** `171490ea1` (the audit run), re-verified against `63a14ebd8`
  (main at report time). Findings already fixed on main are marked **Fixed**.
- **Tooling:** Go 1.26.8 and the CI toolchain Go 1.25.0, golangci-lint v2.13.2,
  govulncheck v1.8.0, actionlint 1.7.12, shellcheck 0.11.0, yarn 1.22.22.
- **Method:** the documented gate (CLAUDE.md "Build / test / gate") run in
  full, plus govulncheck, `yarn audit`, extra linters, a scripted run of
  `metaissue` over every issue-form template, and route-by-route requests
  against `metaserve` on a composed artifact.

## Executive summary

**Overall: healthy core, exposed edges.** The data tooling, the API server and
the intake bot are well built and well tested: the full gate passes, all 25
issue-form cases give the verdict they should, and every API route answers
correctly in under 0.26 s. No injection or output-escaping flaw was found in
the project's own code.

The risk sits around that core, in four places:

1. **Two bugs that bite now, both cheap to fix.** Search and the Audiobookshelf
   provider can't find possessive titles typed without the apostrophe
   ("Enders Game" returns nothing), which affects 5.5% of the catalogue (F1).
   Data merged on 2026-09-23 makes every import emit hundreds of false
   warnings, enough to overflow the PR it opens (Q6). Each is a small, local
   change.
2. **An out-of-support toolchain and stale dependencies.** CI and the image
   build on Go 1.25 (33 known stdlib vulnerabilities, including in the
   `net/http` and `crypto/tls` code metaserve runs on), and the site carries
   one Critical and 16 High dependency advisories (S2, S3).
3. **CI supply-chain hygiene.** Every GitHub Action is referenced by a movable
   tag, several majors behind, including the third-party action that holds the
   intake bot's write token; no job has a timeout; and Dependabot is off, which
   is how the rest went unnoticed (C1-C3, S4).
4. **Data quality debt.** 1,855 groups of duplicate works, with community
   recaps sometimes attached to the wrong copy (D1). This is the existing
   repair-wave roadmap item; the report adds numbers and an order to work in.

**Recommended order:** Q6 and F1 first (hours, visible to users now), then the
Go 1.26 bump, action pinning and Dependabot (an afternoon), then the Astro 7
upgrade (the one sizeable piece). Everything else in the fix list is small and
can follow at any pace. Nothing here calls for rewriting git history or
changing the data model.

**Repository size** (3.2 GB of history, 1.7 GB of data) comes from storing
records in large packs that are re-saved whole on every edit. It is a
contributor inconvenience, not a defect: documenting partial clones fixes it
for contributors today, and moving chapters out of the packs would slow the
growth.

## Summary

| Severity | Open | Fixed on main | Areas |
|---|---|---|---|
| Critical / High | 6 | 1 | Go toolchain, site deps, CI action pinning and versions, job timeouts, search misses apostrophe-less titles |
| Medium | 11 | 0 | Dependabot, server timeouts, CI drift, intake token exposure, image publishing, attachment cap, memory, false import warnings, duplicate works |
| Low / Info | 11 | 0 | headers, redirects, zip limits, workflow hygiene, lint config, test time, intake messages |

### Fix first

1. **Q6** Stop the self-collision ASIN warnings before the next import
   (one-line fix). Data merged on 2026-09-23 makes every import PR body
   overflow GitHub's limit with false warnings.
2. **F1** Match possessive titles typed without the apostrophe
   ("Enders Game"). 5.5% of works are unfindable that way, including through
   the Audiobookshelf provider.
3. **S2** Move to Go 1.26 (go.mod, CI, Dockerfile). Clears 33 stdlib advisories.
4. **C1 + C2 + S4** SHA-pin every action at its current major and add
   `.github/dependabot.yml` (gomod, npm `/site`, github-actions, docker). Turn
   on Dependabot alerts.
5. **S3** Upgrade the site to Astro 7 and Vitest 5. Clears the Critical and most
   Highs.
6. **C3** `timeout-minutes` on every job.
7. **C6** Build intake tools from `main`, not from the rebased PR branch.

## Gate results

| Step | Result | Time |
|---|---|---|
| `go build ./...` | pass | 10 s |
| `go vet ./...` | pass | 3 s |
| `golangci-lint run` (repo default config) | 0 issues | 7 s |
| `go test -race ./...` | pass, 22 packages, 1,557 tests | 382 s |
| `go test ./pkg/check` (non-race real-data suite) | pass | 45 s |
| `metacheck --profile core` | pass | 42 s |
| `metafmt --check --profile core` | pass | 94 s |
| `metabuild` (core only) | pass, 1.66 GB artifact | 97 s |
| site `yarn install` / `build` / `astro check` / `vitest` | pass, 0 errors, 572 tests in 25 files | 29 s |
| actionlint, shellcheck (workflows, `scripts/*.sh`, `.github/scripts/*.sh`) | clean | - |
| `govulncheck ./...` on Go 1.25.0 (CI) | **33 findings** (see S2) | 4 s |
| `yarn audit` (site) | **29 findings** (see S3) | 1 s |

Packages with no tests: the 11 thin `cmd/*` wrappers, `internal/reportdir`,
`internal/testpack` (test-only), and `internal/rawentry` (covered indirectly by
`internal/repair`).

## Security findings

### S1 - HIGH - `golang.org/x/text` infinite loop on invalid UTF-8 - **Fixed**

GO-2026-5970 / CVE-2026-56852 (`norm.Iter` loops forever on some invalid UTF-8)
was reachable through `model.Slugify` (`pkg/model/slug.go:138`) from `metaissue`
(the intake bot), the importers and `metascan`, all of which slug untrusted
text. Fixed on main by `58126337e` (x/text v0.41.0) with a regression test in
`pkg/model/slug_test.go`. No further action.

### S2 - HIGH - CI builds and tests on end-of-life Go 1.25.0

`go.mod` says `go 1.25.0` and every workflow uses `go-version-file: go.mod`, so
setup-go installs exactly 1.25.0 (confirmed in a check.yml run log). Under that
toolchain govulncheck reports 33 reachable stdlib advisories on current main:
crypto/x509 (7), crypto/tls (6), html/template (5), net/url (4), net/http (4)
and others. Under Go 1.26.8 it reports none. Go 1.25 is out of support (current
releases: 1.27.1, 1.26.8). The Dockerfile's `golang:1.25-alpine` floats to
1.25.14, which carries one open advisory and gets no further fixes.

The production binary is the one that matters: `metaserve` is built in the
Docker image, so it serves HTTP with 1.25.x `net/http` and `crypto/tls`.

**Fix:** set `go 1.26` and `toolchain go1.26.8` in go.mod (or `go 1.26.8`),
and change the Dockerfile to `FROM golang:1.26-alpine`. Rerun the gate. The
`KodeStar/audiosilo-meta-sync` pin needs the same toolchain.

### S3 - HIGH (Critical present) - site dependency advisories

`yarn audit` on `site/`: 29 findings, 1 Critical, 16 High, 9 Moderate, 3 Low.

- **Critical:** astro GHSA-26w7-cxv4-gfx2 (AVIF image processing RCE), fixed in
  >= 7.2.8.
- **High:** astro (SSRF, XSS), fast-uri x6 (via `@astrojs/check`), sharp x2,
  postcss, js-yaml x2, nanoid x2, browserslist x2, svgo, smol-toml.
- **Outdated majors:** astro 5.18.2 -> 7.3.4, @astrojs/react 4.4.2 -> 7.0.0,
  vitest 3.2.7 -> 5.0.1, typescript 5.9 -> 7.0.

The site is a static build, so the SSR and server-island advisories only apply
at build time. The image-processing and build-tool advisories still run on CI
runners and contributor machines.

**Fix:** upgrade astro and its integrations to 7.x and vitest to 5.x in one
change, then rerun `astro check` and the vitest suite. Until then, add yarn
`resolutions` for the transitive ones that have patch releases (fast-uri,
nanoid, js-yaml, postcss).

### S4 - MEDIUM - Dependabot alerts disabled, no `dependabot.yml`

`GET /repos/KodeStar/audiosilo-meta/dependabot/alerts` returns 403 "disabled",
and there is no `.github/dependabot.yml`. S1 through S3 and C2 all built up
without anyone being told.

**Fix:** enable Dependabot alerts and security updates in the repository
settings, and add `.github/dependabot.yml` covering `gomod` (`/`), `npm`
(`/site`), `github-actions` (`/`) and `docker` (`/`), weekly, with grouped
minor and patch updates.

### S5 - MEDIUM - `metaserve` sets only `ReadHeaderTimeout`

`internal/serve/serve.go:201` sets `ReadHeaderTimeout: 10s` and nothing else.
A slow request body or an idle keep-alive connection holds a goroutine and a
file descriptor indefinitely. The reverse proxy in front softens this, but the
server shouldn't rely on it.

**Fix:** add `ReadTimeout` (~30 s; every route is a GET), `WriteTimeout`
(sized for the largest response: a whole-series payload or a sitemap page,
~60 s), and `IdleTimeout` (~120 s).

### S6 - LOW - no security headers

No `X-Content-Type-Options: nosniff`, `Referrer-Policy`, or CSP
`frame-ancestors` on HTML or JSON responses.

**Fix:** a small middleware that sets `nosniff` everywhere, and
`Referrer-Policy: strict-origin-when-cross-origin` plus
`Content-Security-Policy: frame-ancestors 'none'` on HTML pages. A full CSP is
a separate job because the Astro shells use inline hydration payloads.

### S7 - LOW - attachment fetch checks the host on the first URL only

`internal/issueform/fetch.go:32` checks the allowlist before the request, but
the client at `fetch.go:36` follows redirects without checking them, and
`github.com` is allowed for any public repository path.

**Fix:** set `CheckRedirect` to require `https` and cap redirects at 3. GitHub
attachments redirect to S3-backed hosts, so rather than pinning every redirect
host, document that the check covers the first hop only. The 1 MiB body cap
(see Q1) still applies after redirects.

### S8 - LOW - unbounded zip entry reads in `pkg/extract`

`pkg/extract/epub.go:668` calls `io.ReadAll` on an epub's zip entry with no
cap. A crafted epub (zip bomb) can exhaust memory. This is a local CLI, so the
impact is limited to whoever runs it.

**Fix:** `io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))`, and error out when
the limit is hit (e.g. 64 MiB).

### Verified OK

- **SQL:** every query is parameterised. The four gosec G202 hits
  (`internal/serve/abs.go:217`, `:250`, `coverage.go:267`, `queries.go:526`)
  build placeholder lists or constant fragments, never user text.
- **Output encoding:** HTML, JSON-LD and embedded hydration payloads are
  escaped in `renderHead`/`renderBody`. The redirect `Location` header is
  `PathEscape`d (the gosec G710 hit is a false positive).
- **Watch feeds:** the `z:` deflate input is size-bounded.
- **Webhook:** HMAC compared in constant time, 1 MiB body cap, 32-byte minimum
  secret.
- **Intake:** attachment 1 MiB cap, sanitised cache tag names, `exec.Command`
  with argument vectors (no shell).
- **Workflows:** only plain `pull_request` triggers, no `pull_request_target`.
- **Site:** the watchlist prototype-pollution hardening landed on main in
  `9dd5c3bb0`, `1217c80e5` and `a7385d4c3`, with tests.

## CI / workflow findings

### C1 - HIGH - actions pinned to mutable tags

All 22 `uses:` lines are major-version tags, including third-party actions
that hold secrets: `peter-evans/create-pull-request@v6` (gets `INTAKE_PAT`) and
`docker/*` (`packages: write`). Whoever controls a tag can repoint it and run
code with those credentials.

**Fix:** pin every action to a full commit SHA with the version in a comment
(`uses: actions/checkout@<sha> # v7.0.1`), and let Dependabot (S4) keep the
SHAs current.

### C2 - HIGH - actions several majors behind

| Action | In use | Current |
|---|---|---|
| actions/checkout | v4 | v7.0.1 |
| actions/setup-go | v5 | v7.0.0 |
| actions/setup-node | v4 | v7.0.0 |
| peter-evans/create-pull-request | v6 | v8.1.1 |
| docker/build-push-action | v6 | v7.4.0 |
| docker/login-action | v3 | v4.6.0 |
| docker/metadata-action | v5 | v6.2.0 |
| docker/setup-buildx-action | v3 | v4.4.1 |

**Fix:** upgrade and SHA-pin together (C1). Read each action's major-version
notes first. checkout and setup-go have changed defaults across these majors.

### C3 - HIGH - no `timeout-minutes` on any job

Every job gets GitHub's 6-hour default. A hung `metaissue` or `metaimport`
would hold an intake concurrency slot and runner minutes for the full 6 hours
(S1's infinite loop was exactly that failure mode).

**Fix:** per-job timeouts from observed durations with headroom: check ~20,
compose ~20, release ~45, intake ~20, rebase sweep ~30, ai-verify ~15,
image ~30.

### C4 - MEDIUM - `persist-credentials` left on

No checkout sets `persist-credentials: false`, so the job token sits in
`.git/config` for every later step, including `go run` over repository code.

**Fix:** `persist-credentials: false` on every checkout except the jobs that
push with git (the rebase sweep). `create-pull-request` manages its own auth.

### C5 - MEDIUM - CI runs less than the documented gate

`check.yml:50` runs `go test ./...` without `-race`, and CI runs no
golangci-lint and no govulncheck, while CLAUDE.md says all of these gate a
change.

**Fix:** add `govulncheck ./...` (fast, and would have caught S1 and S2) and
`golangci-lint run` via the official action at a pinned version. Add `-race`
once Q3 fixes the slow test, or run it in a separate non-blocking job.

### C6 - MEDIUM - intake rebase sweep runs PR-branch code with a write token

`intake.yml:356-383` runs `go run ./cmd/metafmt` and `./cmd/metacheck` from the
rebased PR branch's tree with `INTAKE_PAT` persisted. The sweep selects PRs by
the `bot-intake` label, not by author or changed paths, so a labelled branch
that also changes Go code would run that code with a write token. There is no
`isCrossRepository` filter.

**Fix:** build `metafmt` and `metacheck` from `main` into `$RUNNER_TEMP` before
the loop and call those binaries. Skip PRs whose diff touches anything outside
`data/`, and PRs from forks.

### C7 - MEDIUM - unpinned CLI install next to model credentials

`ai-verify.yml:221` runs `npm install -g @anthropic-ai/claude-code` with no
version, in a job holding `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`.

**Fix:** pin an exact version (`@anthropic-ai/claude-code@<x.y.z>`) and bump
it deliberately.

### C8 - MEDIUM - image publishing has no checks

`image.yml` publishes `latest` (line 47) on any `v*` tag push, without
requiring `check.yml` to pass, without verifying the tag is on `main`, and
without provenance or SBOM.

**Fix:** gate the job on the tag's commit being an ancestor of `origin/main`
and on a green check run for that SHA, and set `provenance: true` and
`sbom: true` on build-push (plus `attestations: write` / `id-token: write`).

### C9 - LOW - `${{ github.repository }}` expanded inside `run:`

`release.yml:193` and `check.yml:105`. The value isn't attacker-controlled, but
expanding expressions into shell text is the pattern that causes injection.

**Fix:** pass it through `env:` and use `"$GITHUB_REPOSITORY"`.

### C10 - LOW - intake concurrency can drop a real run

Intake uses a per-issue concurrency group with `cancel-in-progress`. An
outcome-label `labeled` event (which the job skips) can still take the group
and cancel a pending real run, such as an edit made mid-run. The 21
cancellations in the last 200 runs were benign multi-label bursts.

**Fix:** route events the job will skip to a separate `-noop` concurrency
group, as `ai-verify.yml` already does.

### C11 - LOW - no workflow lint in CI

actionlint is clean today, but nothing keeps it that way.

**Fix:** a small `lint-workflows` job (actionlint + shellcheck) on changes
under `.github/`.

### C12 - INFO - branch protection not verifiable

With read access the protection API returns 404 and rulesets are empty.

**Fix (maintainer):** confirm `check`, `compose` and the site job are required
status checks on `main`.

### CI history

Last 200 runs (2026-09-21 to 09-23): no failures. check 38 ok / 1 cancelled;
release 14 ok / 19 cancelled (superseded queued dispatches, by design); intake
47 ok / 21 cancelled / 14 skipped; ai-verify 17 ok / 8 cancelled / 18 skipped;
image 1 ok.

## Quality findings

### Q1 - MEDIUM - import attachments capped at 1 MiB

`internal/issueform/fetch.go:14` caps every attachment at 1 MiB, with a comment
saying sidecar JSON is small. Sidecars moved to the community repo, and the
import form is now the main user of attachments. A real OpenAudible or
Libation export of a few hundred books is larger than that and gets refused.
The attachment field on the import form is also labelled "Additional notes",
which doesn't tell people to attach the export.

**Fix:** make the cap per template (~25 MiB for import, GitHub's attachment
limit) and relabel the field "Export file" (update `fImportAttachment` and
`labels_test`).

### Q2 - MEDIUM - intake needs 6-10 GB of RAM on the accepted path

Measured on the full core tree: an accepted add-work submission peaked at
**6.1 GB RSS** (75 s), and an accepted import (one pasted Libation book) at
**10.3 GB RSS** (127 s), because the accepted path re-validates the whole tree
after writing. Rejected submissions return in ~40 s. GitHub runners have
16 GB, so CI works today with ~5 GB headroom on imports, but a contributor on
an 8 GB machine can't reproduce an intake run locally (an earlier attempt here
was OOM-killed).

**Fix:** re-validate only the packs the submission touched plus the
cross-record indexes (the loader is already per-pack), or document the memory
requirement in CONTRIBUTING.

### Q3 - LOW - `pkg/canonical` real-data test takes 352 s under `-race`

Over half the 10-minute default `go test` timeout, and the reason CI can't
cheaply add `-race` (C5).

**Fix:** skip the real-data test under the race detector, as `pkg/check`
already does (`raceEnabled`), or gate it on `-short`.

### Q4 - LOW - no committed linter config

There's no `.golangci.yml`, so "golangci-lint at a green baseline" means the
defaults. Enabling more linters finds: gosec 240 (mostly G304/G306/G301 file
permissions, many in tests), noctx 141 (`internal/build` uses `Exec` rather
than `ExecContext`), bodyclose 37 (tests only), gocyclo (`pkg/extract/html.go`
`renderHTML` at 33), nestif 9 (e.g. `internal/issueform/refs.go:392`,
`internal/serve/abs.go:109`).

**Fix:** commit a `.golangci.yml` that pins the current linter set, then add
`noctx` and `bodyclose` (low noise, real value) with fixes. Tune gosec's file
permission rules rather than enabling them wholesale.

### Q5 - INFO - CLAUDE.md size

CLAUDE.md was 205 KB (~50k tokens loaded into every assistant session).
Trimmed to its essentials in a separate commit in this PR.

### Q6 - MEDIUM - false ASIN-collision warnings flood every import

`internal/importer/importer.go:912` `locateASIN` is called once per
`{asin, region}` entry (`importer.go:831`). A recording that carries the same
ASIN in several marketplaces collides with *itself* and emits
`catalogue: ASIN X is recorded on both W/R and W/R`. The comment above it
assumes "no such pair exists in the catalogue today", which stopped being true
when PRs #2318 and #2319 (merged 2026-09-23) put up to 11 region entries of one
ASIN on one recording.

Measured on the `171490ea1` tree (the importer is unchanged on main): one
accepted single-book import returned 379 messages,
**377 of them this self-collision warning** (84 KB). `intake.yml` writes every
message into the PR body's Notes list, and GitHub caps a PR body at 65,536
characters, so the next import PR is truncated or fails to open, and any real
warning is lost in the noise. Import PRs before 2026-09-23 (#2276, #2277) show
none. The same warning reaches `metaimport --enrich` runs in the sync bot.

**Fix:** in `locateASIN`, return early when `prev == RecRef{workSlug, recSlug}`,
with a test on a multi-region recording. As a guard, cap the Notes list in
`intake.yml` (e.g. the first 50 messages plus "and N more").

### Q7 - LOW - intake messages that mislead

- A runtime correction against a work URL says `field "runtime_min" on a work
  cannot be auto-corrected (only simple scalar fields are)`. The real reason is
  that runtime lives on a recording. **Fix:** name the recording path in the
  message.
- A correction setting `license` to `MIT` goes to needs-human with "a
  maintainer will apply it". The schema only allows `CC0-1.0` in core, so it can
  never be applied. **Fix:** refuse license (and other enum) values outside
  the schema as `invalid`.
- Accepted import PRs show an empty "Files:" list, because the import path
  writes the tree directly and reports no file list (`intake.yml`, PR body
  step). **Fix:** fill the list from `git status --porcelain data/`.
- A malformed ASIN is dropped with a note and the record is still created
  (`value "NOTANASIN" is not a valid ASIN - skipped`). That leniency is
  reasonable, but a submission whose *only* identifier is malformed arguably
  deserves needs-human rather than ok.

## Issue forms

Each template was run through `metaissue --profile core` on a copy of the data
tree, as the intake bot runs it.

Peak memory and wall time are in Q2.

| Template | Case | Verdict | Behaviour |
|---|---|---|---|
| add-work | new book | ok | work, author and narrator written (3 files) |
| add-work | exact duplicate of a bulk-mirror record | needs-human | "should replace what is recorded there - a maintainer will apply it" (trust tiers) |
| add-work | title with "(Unabridged)" | needs-human | decoration stripped, then caught as the same duplicate |
| add-work | ASIN already catalogued | needs-human | matched to the existing recording |
| add-work | missing title | invalid | "Title is required" |
| add-work | language `klingonese` | invalid | not a BCP-47 code |
| add-work | malformed ASIN | ok | ASIN dropped with a note, record created (Q7) |
| add-work | title "Search" (reserved slug) | ok | slug stepped off to `search-harness-reservedauthor` |
| add-work | author "ChatGPT" | invalid | AI author refused |
| add-work | narrator "Virtual Voice" | ok | folded onto `virtual-voice` (`kind: synthetic`); the first such submission re-creates that record, removed as orphaned on 2026-08-02 |
| add-work | CC0 box unticked | invalid | refused |
| add-recording | new narration, work by URL | ok | 2 files |
| add-recording | new narration, work by bare slug | ok | 2 files |
| add-recording | unknown work | needs-human | "submit an Add a work form first" |
| correct-data | runtime against a work URL | needs-human | message misleading (Q7) |
| correct-data | runtime via retired file-per-record path | ok | resolved to the pack entry and applied |
| correct-data | `license` to `MIT` | needs-human | should be invalid (Q7) |
| correct-data | unknown record | needs-human | "a maintainer will locate the right record" |
| import | pasted Libation JSON | ok | 1 work, 1 recording, 2 people; **377 false warnings (Q6)** |
| import | folder-scan export | needs-human | maintainer runs metascan/metaimport, by design |
| import | attachment on a non-GitHub host | invalid | host allowlist refused it |
| import | no attachment or JSON | invalid | refused |
| (sidecar) | `data:characters` label on core | needs-human | redirected to the community repo |
| (none) | no routing label | invalid | refused |

All 25 behaved as designed, apart from the messages noted in Q6 and Q7.

## Functional smoke test

`metabuild -data data --community <community>/data` (community at `029ebdd`)
built in 73 s: 278,582 works, 292,467 recordings, 123,330 people, 45,203
series, 43,568 characters, 19,182 recaps, 471 descriptions, schema_version 6.
`metaserve --db ... --site site/dist` was then asked for every route in
`openapi.json` plus the HTML, sitemap and site routes.

- **Every route answered correctly**: 200 on valid input; 400 for an empty
  query, a missing lookup key, a watch feed with no or undecodable `s`, and
  `/abs/search` with no query; 404 for unknown ids, a bad ASIN and an
  out-of-range sitemap; 301 for retired work, person and series slugs on both
  API and HTML routes. The release webhook route is absent when no secret is
  set.
- **Fast:** every response under 0.26 s. The slowest were
  `/api/v1/coverage/works` (0.25 s), a one-letter search (0.21 s) and
  `series-gaps` (0.13 s). Entity pages render server-side in 1-5 ms.
- **Sitemaps:** 13 files in the index, 50,000 URLs (6.2 MB) per works file,
  within the protocol limits.
- **Hostile input:** a quote-and-operator query (`"')(*`) and a 5,000-character
  query both returned a clean empty result.
- Only `Access-Control-Allow-Origin: *` is set; no security headers (S6).

The run surfaced two findings:

### F1 - HIGH (functional) - possessive titles can't be found without the apostrophe

The FTS index uses the default `unicode61` tokenizer, which splits "Ender's"
into `ender` + `s`. A query for `enders` matches neither. Both search and the
Audiobookshelf provider miss the book entirely:

| Query | Result |
|---|---|
| `/api/v1/works/search?q=enders game` | 0 |
| `/api/v1/works/search?q=ender's game` | 4 |
| `/api/v1/works/search?q=hitchhikers guide` | 0 |
| `/abs/search?query=Enders Game&author=Orson Scott Card` | 0 |
| `/abs/search?query=The Hitchhikers Guide to the Galaxy&author=Douglas Adams` | 0 |
| `/abs/search?query=Harry Potter and the Philosophers Stone&author=J.K. Rowling` | 0 |
| each of the above with the apostrophe | 4-6 |

**15,294 work titles (5.5%) and 1,450 series names** contain a possessive.
Audiobookshelf queries come from folder and file names, which often leave out
apostrophes, so this directly lowers the match rate for the priority consumer.
Worse, `magicians nephew` returns only a malformed duplicate record
(`06 The Magicians Nephew`, see D1), because that record is the one title
spelled without the apostrophe.

**Fix (query side, no rebuild):** in `tokenPhrases`
(`internal/serve/search.go`), expand a term longer than 3 runes that ends in
`s` into `("enders" OR "ender s")`, joined to the other terms with an explicit
`AND` (FTS5 rejects implicit AND after a parenthesised group). Checked
directly against the artifact: `("enders" OR "ender s") AND "game"` returns 5
rows where the current query returns 0, and
`("hitchhikers" OR "hitchhiker s") AND "guide"` returns 36. Measure it against
`TestServeLookupsAreIndexed` and the existing cost gates, and add the three
queries above as regression cases. An index-side alternative (also indexing an
apostrophe-folded copy of each title) needs a rebuild but no query change.

### D1 - MEDIUM (data) - duplicate and malformed work records

metabuild's advisory summary on the composed tree: **1,855
normalized-identity duplicate work groups**, 321 identity-equal work pairs,
85 honorific person pairs, 244 cross-language recordings, 16 orphan people,
18 oversized entries and **400 mis-scaled sidecars** (community positions out
of step with the core recording's chapter scale). **593 work titles** start
with a two-digit track number (`06 The Magicians Nephew`).

A worked example: "The Magician's Nephew" is 7 work records, 6 of them the
same English book (`the-magicians-nephew`, `-unabridged`, `-abridged`,
`-chronicles-of-narnia-book-1`, `the-chronicles-of-narnia-the-magicians-nephew`,
`06-the-magicians-nephew`). The community characters and recaps are attached
to the malformed `06-` record, so the correctly titled page has none, and F1
means only the malformed one comes up in search.

**Fix:** this is the roadmap's "repair waves over the audit's duplicate
clusters". Prioritise clusters that hold community sidecars, so the recaps
move to the surviving record through the slug tombstone, and add a
`titlerule` rule and audit proposal for leading track numbers.

## Repository size

Why a clone is ~5 GB:

- **`.git` is 3.2 GB** (3.13 GiB pack, 229,794 objects). History under
  `data/works` alone is 148,768 blobs, 39.7 GB uncompressed, 3.2 GB on disk.
  The average blob is ~267 KB, a whole pack file: each edit to one work
  re-stores the full 256-512 KB pack it lives in, and git's delta compression
  only partly makes up for that. Other families are small (series 48 MB,
  works-community history 37 MB, people 35 MB).
- **The working tree `data/` is 1.7 GB** (works 1.6 GB in 4,630 packs,
  ~142k works). In a sample, chapter lists were 71% of the compacted bytes, and
  the canonical 2-space pretty JSON is 2.02x the compact size.
- Code is under 10 MB. `site/node_modules` (331 MB) is local and gitignored.

**Recommendations:**

1. **Now:** document partial and shallow clones in CONTRIBUTING
   (`git clone --filter=blob:none`, or `--depth 1` for one-off
   contributions). No repository change needed.
2. **Measure:** a smaller pack target (e.g. 128 KB) re-stores less per edit at
   the cost of more files. `metafmt --write` does the rebinding.
3. **Consider:** moving chapters into their own family, or a compact rendering
   with one chapter per line. Chapters are most of the bytes and rarely change
   once written.
4. **Don't** rewrite history: slugs and URLs are public API, and forks and the
   sync bot depend on the existing commits.

## Prioritised fix list

| # | Finding | Effort | Where |
|---|---|---|---|
| 1 | Q6 skip self-collision ASIN warning (+ cap PR notes) | S | `internal/importer/importer.go`, `intake.yml` |
| 2 | F1 possessive search expansion | S | `internal/serve/search.go` |
| 3 | S2 Go 1.26 toolchain | S | go.mod, Dockerfile, sync-bot pin |
| 4 | C1 + C2 SHA-pin and upgrade actions | M | `.github/workflows/*` |
| 5 | S4 Dependabot alerts + config | S | settings, `.github/dependabot.yml` |
| 6 | S3 Astro 7 / Vitest 5 | M | `site/` |
| 7 | C3 job timeouts | S | `.github/workflows/*` |
| 8 | C6 intake sweep builds tools from main | S | `intake.yml` |
| 9 | C5 govulncheck + golangci-lint in CI | S | `check.yml` |
| 10 | C4 persist-credentials false | S | `.github/workflows/*` |
| 11 | C7 pin the CLI in ai-verify | S | `ai-verify.yml` |
| 12 | C8 gated, attested image publish | M | `image.yml` |
| 13 | S5 server timeouts | S | `internal/serve/serve.go` |
| 14 | Q1 import attachment cap + label | S | `internal/issueform`, issue template |
| 15 | D1 repair waves, sidecar-bearing clusters first; track-number rule | L | `internal/audit`, `internal/repair`, `internal/titlerule` |
| 16 | Q2 scoped re-validation or documented RAM | M | `internal/issueform`, `pkg/check` |
| 17 | Q3 race-skip slow test | S | `pkg/canonical` |
| 18 | Q4 `.golangci.yml` | S | repo root |
| 19 | Q7, S6, S7, S8, C9, C10, C11 | S each | as listed |
| 20 | Repo size: document partial clone | S | CONTRIBUTING |
