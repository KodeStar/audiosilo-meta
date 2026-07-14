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
Libation/folder-scan field mappings + the existing-work matching/routing) and
`src/lib/github-prefill.ts` (the prefilled issue-form URLs + the factual-subset
privacy contract).

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
| `/` | `src/pages/index.astro` | Landing page - search hero, stats band, latest additions, contribute band |
| `/work?id=<id>` | `src/pages/work.astro` | A work: cover, authors, series, and each recording (narrators, runtime, publisher, ASIN/ISBN chips, expandable chapters) |
| `/person?id=<id>` | `src/pages/person.astro` | A person: works they wrote and audiobooks they narrated |
| `/series?id=<id>` | `src/pages/series.astro` | A series: authors and works in reading order |
| `/import` | `src/pages/import.astro` | In-browser library diff (OpenAudible, Libation, or a metascan folder scan): which of your books are catalogued and which are new, with prefilled contribution issues |
| `/contribute` | `src/pages/contribute.astro` | Contribution coverage: stats band, books needing characters/recaps (grouped by series, linking into `/build`), and series with missing volumes |
| `/build?work=<id>&kind=characters\|recaps` | `src/pages/build.astro` | Guided characters/recaps builder - edit the expressive layer, download the sidecar JSON, and open a prefilled contribution issue |
| `/404` | `src/pages/404.astro` | On-brand not-found page |

The three detail pages are static shells that hydrate a `client:only` React
island reading the `?id=` query parameter, so they load instantly and fetch on
the client. They handle their own loading, not-found (404), and error states.

## Structure

```
src/
  lib/api.ts              the ONE typed API client (wire shapes + fetch + formatters)
  layouts/Base.astro      shell: head/meta, header, footer, skip link, scroll-reveal
  components/
    Header.astro          sticky nav (Contribute, Docs, audiosilo.app, Discord, GitHub)
    Footer.astro          Data / Community / AudioSilo columns + CC0 note
    Logo.astro            brand logo (rotated 180deg - the raw SVG is upside down)
    Icon.astro Waveform.astro Button.astro   shared primitives
    search/SearchBox.tsx  debounced combobox island (grouped results, keyboard nav, ASIN/ISBN lookup)
    home/                 StatsBand.tsx, LatestAdditions.tsx (islands), Hero.astro, Contribute.astro
    cards/                WorkCard, CoverImage (fallback tile), PersonLinks, SeriesBadge
    detail/               WorkDetail, PersonDetail, SeriesDetail + shared detail-common
  pages/                  index, work, person, series, 404
  styles/global.css       Tailwind v4 entry + @theme design tokens (mirrors the product)
mock/                     throwaway local API + fixtures (not shipped)
public/                   logo, favicons, CNAME (meta.audiosilo.app), robots.txt
```

## Conventions

- **Copy is true to what exists.** The in-browser import page is labelled
  "coming soon"; the command-line importer exists today. Data is CC0; the code
  is AGPL-3.0.
- **Dark-only**, cinematic, pink `#db2777` as an accent (not a paint bucket).
- **Hyphens, never em dashes.** British-neutral English.
- Animations respect `prefers-reduced-motion`; scroll-reveal degrades to fully
  visible without JavaScript.
