// The guard on the injection contract: metaserve renders the entity pages by
// splitting the BUILT shells on these markers, so if a Base.astro or page-shell
// refactor drops one, Phase A silently turns off - the server would serve an
// untouched client-only shell and every /works/{slug} would look empty to a
// crawler, with nothing failing anywhere.
//
// It asserts over dist/ rather than over the .astro sources on purpose: what the
// server splits is the built output, and a build step that moved or stripped a
// comment would leave the sources looking perfectly correct. Runs only after
// `yarn build`; skipped with a message otherwise, so `yarn test` alone stays
// useful. The full gate is `yarn build && yarn run check && yarn test`.

import { describe, it, expect } from 'vitest'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// The contract, spelled here as literals rather than imported: this test is the
// thing that notices when the markers in Base.astro / the page shells change,
// and a shared constant would let both sides move together silently. Keep in
// step with internal/serve.
const HEAD_OPEN = '<!--ssr:head-->'
const HEAD_CLOSE = '<!--/ssr:head-->'
const ENTITY = '<!--ssr:entity-->'

const shells = ['work', 'person', 'series'] as const

function distPath(page: string): string {
  return fileURLToPath(new URL(`../../dist/${page}/index.html`, import.meta.url))
}

function occurrences(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1
}

describe('the built detail shells carry the injection markers', () => {
  for (const page of shells) {
    const path = distPath(page)

    it(`dist/${page}/index.html`, (ctx) => {
      if (!existsSync(path)) {
        ctx.skip(`no build output at dist/${page}/index.html - run \`yarn build\` first`)
        return
      }
      const html = readFileSync(path, 'utf8')

      // Exactly once each: two head-open markers would make the server's split
      // ambiguous, and zero would make it a no-op.
      expect(occurrences(html, HEAD_OPEN)).toBe(1)
      expect(occurrences(html, HEAD_CLOSE)).toBe(1)
      expect(occurrences(html, ENTITY)).toBe(1)

      const open = html.indexOf(HEAD_OPEN)
      const close = html.indexOf(HEAD_CLOSE)
      // In order, and inside <head> - the block the server replaces is head
      // content, and an opener after its closer describes an empty region.
      expect(open).toBeLessThan(close)
      expect(close).toBeLessThan(html.indexOf('</head>'))

      // The swappable block really holds the metadata the server composes: the
      // title is the marker of the whole set, and a refactor that moved it out
      // (leaving the markers wrapping nothing) would pass every check above.
      const headBlock = html.slice(open, close)
      expect(headBlock).toContain('<title>')
      expect(headBlock).toContain('name="description"')
      expect(headBlock).toContain('rel="canonical"')
      expect(headBlock).toContain('property="og:title"')
      expect(headBlock).toContain('name="twitter:card"')

      // ...and the document-wide tags stay OUTSIDE it, so the swap cannot take
      // the charset or the favicons with it.
      expect(headBlock).not.toContain('charset')
      expect(headBlock).not.toContain('rel="icon"')
      expect(headBlock).not.toContain('name="viewport"')

      // The body marker sits in the body, after the head block it follows.
      expect(html.indexOf(ENTITY)).toBeGreaterThan(html.indexOf('</head>'))
    })
  }
})
