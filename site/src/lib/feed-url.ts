import { API_BASE } from './api'
import type { Watchlist } from './watchlist'

export type FeedFormat = 'atom' | 'json'

export interface FeedURLs {
  atom: string
  json: string
}

const PLAIN_URL_LIMIT = 1500

/** The server's contract limit, stated once on this side too
    (`maxWatchSeries`, internal/serve/seriesparam.go; openapi.json's `s`
    parameter). Refusing here means a reader past it is told so on the page,
    rather than being handed a URL that 400s inside their feed reader. */
export const MAX_FEED_SERIES = 200

function watchedSlugs(store: Watchlist): string[] {
  return Object.entries(store.series)
    .filter(([, series]) => !series.hidden)
    .map(([slug]) => slug)
    .sort()
}

function endpoint(format: FeedFormat, origin: string, apiBase: string): URL {
  const base = apiBase ? new URL(apiBase, origin).toString() : origin
  return new URL(`${base.replace(/\/$/, '')}/api/v1/watch/feed.${format}`)
}

function withSeries(endpoint: URL, series: string): string {
  endpoint.searchParams.set('s', series)
  return endpoint.toString()
}

function base64url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function compactSeries(csv: string): Promise<string> {
  const input = new Blob([new TextEncoder().encode(csv)]).stream()
  const compressed = input.pipeThrough(new CompressionStream('deflate-raw'))
  const bytes = new Uint8Array(await new Response(compressed).arrayBuffer())
  return `z:${base64url(bytes)}`
}

/**
 * Build the two stateless subscription URLs for a watchlist. The readable CSV
 * form is preferred while the complete URL stays below 1500 characters; a
 * longer list uses the server's raw-DEFLATE compact form. Hidden series never
 * leave the browser.
 *
 * Throws with a reader-facing message when the list cannot become a feed - no
 * visible series, or more than the server accepts.
 */
export async function buildFeedURLs(
  store: Watchlist,
  origin = globalThis.location.origin,
  apiBase = API_BASE
): Promise<FeedURLs> {
  const slugs = watchedSlugs(store)
  if (slugs.length === 0) throw new Error('at least one visible series is required')
  if (slugs.length > MAX_FEED_SERIES) {
    throw new Error(
      `A feed URL can carry ${MAX_FEED_SERIES} series; you are watching ${slugs.length}. Hide some to build one.`
    )
  }
  const csv = slugs.join(',')

  // One endpoint each, measured once: the two URLs differ only in the four
  // characters of their extension, so the Atom one's length decides both.
  const atom = endpoint('atom', origin, apiBase)
  const json = endpoint('json', origin, apiBase)
  const series =
    withSeries(atom, csv).length < PLAIN_URL_LIMIT ? csv : await compactSeries(csv)
  return { atom: withSeries(atom, series), json: withSeries(json, series) }
}
