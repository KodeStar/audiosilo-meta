# audiosilo-meta site

The public face of the AudioSilo community audiobook metadata database at
[meta.audiosilo.app](https://meta.audiosilo.app) - search, browse, and contribute
to the open catalogue of works, recordings, narrators, series and chapters.

It is a static Astro site with a handful of React islands. The Go API server
(built alongside, in the parent repository) serves this compiled site
**same-origin** and answers the `/api/v1/*` endpoints the islands read from.

## Stack

- **Astro 5** (static output) - most of the page is server-rendered HTML.
- **React 19 islands** only where runtime state is needed: the search box, the
  home stats/latest bands, and the three detail views.
- **Tailwind CSS v4** via `@tailwindcss/vite`; design tokens live in
  `src/styles/global.css` `@theme` (there is no `tailwind.config`). The palette
  and motifs mirror the AudioSilo product so this reads as a sibling of
  audiosilo.app.
- **Roboto** via `@fontsource/roboto`.
- Package manager is **yarn** (`yarn.lock` committed); **Node 24**.

## Commands

```sh
# Node 24 (workspace convention):
export PATH="$HOME/.nvm/versions/node/v24.16.0/bin:$PATH"

yarn install
yarn dev          # http://localhost:4321 (Vite dev server)
yarn build        # static site -> dist/
yarn run check    # astro check (TypeScript) - note `yarn run`, since bare
                  # `yarn check` runs yarn's own lockfile check instead
yarn test         # vitest run (unit tests for the pure logic modules)
yarn preview      # serve the built dist/ locally
```

**The gate (run before a change is done):** `yarn build && yarn run check && yarn test`.

## Tests

[Vitest](https://vitest.dev) covers the site's pure, framework-free logic
modules (no DOM, no React - the `node` environment). Tests are co-located as
`src/**/*.test.ts` and use explicit `import { describe, it, expect } from
'vitest'` (globals stay off, so `astro check` needs no extra type config). Run
them with `yarn test` (one-shot) or `yarn test:watch` (watch mode).

Current coverage: `src/lib/import-parse.ts` (export detection + the OpenAudible/
Libation/Audiobookshelf/folder-scan field mappings + the existing-work matching/
routing), `src/lib/github-prefill.ts` (the prefilled issue-form URLs + the
factual-subset privacy contract) and `src/lib/entity-url.ts` (the path-route /
legacy `?id=` slug rule). `src/lib/dist-markers.test.ts` is the one test that
reads build OUTPUT rather than a module - the metaserve injection guard below -
so it needs `yarn build` to have run and skips cleanly when it has not.

## The API base (`PUBLIC_API_BASE`)

Every island fetches through the one typed client, `src/lib/api.ts`. It reads
its base URL from the build-time env var `PUBLIC_API_BASE`:

- **Unset / empty (default, production):** requests are same-origin
  (`/api/v1/...`). This is correct because the Go server hosts the built site.
- **Set to an absolute URL:** requests target that host - used to develop the
  site against an API running on a different port. The value is baked into the
  client bundle at build time (Astro only exposes `PUBLIC_`-prefixed vars to the
  browser), so set it before `yarn build` / `yarn dev`.

### Running the site against a local API

Against the real Go server (which serves the site same-origin, so no base is
needed) - run the server, then open the address it serves.

Against a separate API process during frontend work, point the site at it and
make sure that API sends permissive CORS headers for the dev origin:

```sh
PUBLIC_API_BASE=http://localhost:8099 yarn dev      # dev loop
PUBLIC_API_BASE=http://localhost:8099 yarn build    # or a static build to preview
```

### Local mock API (visual development only)

`mock/` contains a dependency-free mock of the whole API contract, backed by
`mock/fixtures.json`. It is **not shipped** and nothing in `src/` imports it -
the site only ever talks to `src/lib/api.ts`. Use it to see populated pages:

```sh
node mock/server.mjs                              # http://localhost:8099 (+ CORS)
PUBLIC_API_BASE=http://localhost:8099 yarn dev    # in another terminal
```

Fixtures cover: `Project Hail Mary` (single recording, chapters, multi-region
ASINs), `Harry Potter and the Philosopher's Stone` (two recordings - Stephen Fry
and Jim Dale), and `The Stormlight Archive` (a two-book series). Try the ASIN
`B08G9PRS1K` in the search box to see the pinned exact-match lookup.

## Page map

| Route | File | What it is |
|---|---|---|
| `/` | `src/pages/index.astro` | Landing page - photographic search hero, stats band (characters/recaps lead the catalogue totals), latest additions, what-it-is cards, contribute band |
| `/works/<id>` | `src/pages/work.astro` | A work: cover, authors, series, and each recording (narrators, runtime, publisher, ASIN/ISBN chips, expandable chapters) |
| `/people/<id>` | `src/pages/person.astro` | A person: works they wrote and audiobooks they narrated |
| `/series/<id>` | `src/pages/series.astro` | A series: authors and works in reading order |
| `/import` | `src/pages/import.astro` | In-browser library diff (OpenAudible, Libation, an Audiobookshelf export, or a metascan folder scan): which of your books are catalogued and which are new, with prefilled contribution issues |
| `/audiobookshelf` | `src/pages/audiobookshelf.astro` | Audiobookshelf integration: add the database as an ABS custom metadata provider, and export an ABS library into `/import` |
| `/contribute` | `src/pages/contribute.astro` | Contribution coverage: clickable stats band, a filterable/searchable/paginated coverage browser (needs-work vs has-characters/story-so-far/recap-summary, linking into `/build`), and a searchable/paginated list of series with missing volumes |
| `/build?work=<id>&kind=characters\|recaps` | `src/pages/build.astro` | Guided characters/recaps builder - edit the expressive layer, download the sidecar JSON, and open a prefilled contribution issue |
| `/privacy` | `src/pages/privacy.astro` | Privacy policy for the site, the API and the data: no accounts/cookies/analytics, what the server logs, what stays in the browser (`/import`), and the third parties a page can reach (`/add`'s libex lookup, cover-image hosts) |
| `/404` | `src/pages/404.astro` | On-brand not-found page, with add-it CTAs |

The three detail pages are the three routes metaserve renders server-side (see
below). Their built files stay at `dist/work|person|series/index.html`, which is
also where the **legacy** query-parameter URLs land: `/work?id=<id>`,
`/person?id=<id>` and `/series?id=<id>` 301 to the path routes and are kept
alive forever, because every link ever shared, every stored `books.work_id` and
every prefilled issue holds one. The shells are static, hydrate a `client:only`
React island, and handle their own loading, not-found (404) and error states.

### The metaserve injection contract

On an entity route metaserve serves the BUILT shell with two regions replaced,
so the site keeps one design truth (Astro) and the server owns no parallel page:

| Marker (in the built HTML) | Where it comes from | What the server puts there |
|---|---|---|
| `<!--ssr:head-->` ... `<!--/ssr:head-->` | `src/layouts/Base.astro` | the entity's title, description, canonical, og/twitter set |
| `<!--ssr:entity-->` | the three page shells, via `ENTITY_MARKER` (`src/lib/ssr-markers.ts`) | `<div id="ssr-entity">` (a crawler-first fact sheet) + `<script type="application/json" id="entity-data">` (the entity JSON, byte-for-byte what the matching API route returns) |

Rules that make it safe:

- **The markers are inert comments.** Every page carries the head pair; only the
  three detail shells carry the body marker. With nothing injected - any other
  page, an older metaserve, `yarn preview` - the shell is served exactly as
  built and the island behaves as it always did.
- **Only entity-swappable metadata goes between the head markers.** charset,
  viewport, favicons, theme-color and the generator tag stay outside, above.
- **The island prefers the embedded payload.** `useEmbeddedEntity`
  (`src/components/detail/detail-common.tsx`) parses `#entity-data` and uses it
  only when its `id` matches the slug in the URL; it then skips the initial
  fetch, removes the `#ssr-entity` fact sheet, and leaves `document.title`
  alone (the server's composed title is the richer one). Secondary reads -
  chapters on expand, the work page's series rail - are unchanged.
- **Slugs come from the path first** (`entitySlugFromLocation`,
  `src/lib/entity-url.ts`), `?id=` second.
- **`yarn dev` and `yarn preview` have no Go server**, so there are no path
  routes and no injection there: develop a detail page through the legacy
  `/work?id=<id>` form, which is exactly the fallback the code keeps working.
- `src/lib/dist-markers.test.ts` reads `dist/` after a build and fails if a
  marker is missing, duplicated, out of order, or no longer wrapping the head
  metadata - the guard that a `Base.astro` refactor cannot silently turn the
  server-rendered pages back into empty shells. It skips (with a message) when
  `dist/` has not been built, so run the gate in order:
  `yarn build && yarn run check && yarn test`.

## Structure

```
src/
  lib/api.ts              the ONE typed API client (wire shapes + fetch + formatters)
  lib/hero.ts             the hero photograph: file, treatment nudges, and its licence attribution
  layouts/Base.astro      shell: head/meta, header, footer, skip link, scroll-reveal
  components/
    Header.astro          sticky nav + the site-wide search (Contribute, Docs, audiosilo.app, Discord, GitHub)
    Footer.astro          Data / Community / AudioSilo columns + CC0 note + the hero photo credit
    Backdrop.astro        the shared photographic backdrop (hero + page-header band)
    Logo.astro            brand logo (rotated 180deg - the raw SVG is upside down)
    Icon.astro Waveform.astro Button.astro   shared primitives
    search/SearchBox.tsx  debounced combobox island (kind tabs with counts, keyboard nav, ASIN/ISBN lookup)
    search/HeaderSearch.tsx  the header's copy of it; on home it appears once the hero's box scrolls away
    home/                 StatsBand.tsx, LatestAdditions.tsx (islands), Hero.astro, ValueCards.astro, Contribute.astro
    cards/                WorkCard, CoverImage (fallback tile), PersonLinks, SeriesBadge
    detail/               WorkDetail, PersonDetail, SeriesDetail + shared detail-common
  pages/                  index, work, person, series, 404
  styles/global.css       Tailwind v4 entry + @theme design tokens (mirrors the product)
mock/                     throwaway local API + fixtures (not shipped)
public/                   logo, favicons, CNAME (meta.audiosilo.app), robots.txt
public/img/hero/          the hero photograph in three width tiers (greyscale webp)
public/img/card/          card art (greyscale webp, generated - no attribution owed)
```

## Conventions

- **Copy is true to what exists.** The in-browser import page is live (it parses
  OpenAudible, Libation, Audiobookshelf, and metascan folder-scan exports client-
  side); the command-line importer exists alongside it. The factual data core is
  CC0; the characters/recaps layer is CC BY-SA 3.0; the code is AGPL-3.0.
- **Dark-only**, cinematic, pink `#db2777` as an accent (not a paint bucket).
- **Hyphens, never em dashes.** British-neutral English.
- Animations respect `prefers-reduced-motion`; scroll-reveal degrades to fully
  visible without JavaScript.
- **The hero photograph carries a licence obligation.** It is CC BY-SA 4.0, so
  the footer credit (title, author, source link, licence link, and a statement
  of the changes made) is required, not decorative - and because it is
  share-alike, our modified version is offered under the same terms. All of it
  is generated from `src/lib/hero.ts`, where `attribution` is a REQUIRED field
  precisely so a replacement image cannot ship with the credit silently absent.
  Verify a new image's author against the source's own metadata, never a
  third-party search index. The card art is generated and owes nothing.
- **Photographic art is monochrome by encoding, not by CSS.** The files are
  greyscale webp because the design renders them that way, and dropping the
  chroma planes is a saving no filter can give back. Images sit behind copy at
  low opacity with a mask that clears the text, so art is composed
  subject-upper-right, dark-lower-left.
- **Do not put `scroll-padding-top` on `html`.** An input inside the sticky
  header can never satisfy it: the browser scrolls the focused element into view
  on every keystroke, the header travels with the scroll, and the page walks to
  the top one character at a time. Anchor offsets belong on the targets
  (`:where([id]) { scroll-margin-top }`), which `global.css` does.
