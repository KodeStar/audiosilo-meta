// @ts-check
import { defineConfig } from 'astro/config'
import react from '@astrojs/react'
import sitemap from '@astrojs/sitemap'
import tailwindcss from '@tailwindcss/vite'

// https://astro.build/config
export default defineConfig({
  site: 'https://meta.audiosilo.app',
  output: 'static',
  // Astro 7's default ('jsx') drops a line break between an inline element and
  // the text beside it, and the templates wrap prose across lines: under it the
  // pages read "thecontributing guideand thelicensing". Keep the old collapsing.
  compressHTML: true,
  integrations: [
    react(),
    sitemap({
      // The flat detail and guide shells are hydration targets for metaserve's
      // server-rendered entity routes (/works/{id}, /works/{id}/recap, ...),
      // not pages of their own: visited bare they render the island's
      // not-found state. Listing them in the static sitemap advertises
      // soft-404s - and for /recap/ and /characters/, soft-404s titled for the
      // very queries the guide pages exist to win. The real entity URLs are
      // listed by metaserve's own /sitemaps/ shards.
      filter: (page) =>
        !['/work/', '/person/', '/series/', '/recap/', '/characters/'].some((shell) =>
          page.endsWith(shell)
        ),
    }),
  ],
  // package.json lists `vite` as a devDependency ONLY because vitest declares it
  // as a peer and yarn 1 does not install peers. Astro brings its own vite, so
  // that entry must stay inside astro's vite range (one copy in yarn.lock);
  // dependabot ignores vite majors so one can never land ahead of astro.
  vite: {
    plugins: [tailwindcss()],
    // /docs/api renders internal/serve/openapi.json, which sits one level above
    // this package: the spec is authored in the Go tree and served verbatim by
    // metaserve, so the page reads THAT file rather than a copy that could drift.
    // The production build resolves it either way; this is what additionally
    // lets `astro dev` read it.
    //
    // Scoped to internal/ rather than the whole repo: '..' would let the dev
    // server hand out anything in the checkout - the entire data/ tree, a local
    // meta.sqlite, a .env - over a port that is often bound to more than
    // localhost. The page needs exactly one file, and it is under here.
    //
    // '.' (this package, resolved against the Vite root) must be listed too: an
    // explicit allow list REPLACES Vite's default, so without it every /src
    // module an island loads answers 403 in dev and no island hydrates.
    server: { fs: { allow: ['.', '../internal'] } },
  },
})
