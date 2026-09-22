import { API_BASE } from './api'
import type { Watchlist } from './watchlist'

export interface FeedURLs {
  atom: string
  json: string
  /** The calendar subscription, `GET /api/v1/watch/releases.ics` - the same
      `s`/`window` parameters, rendered as iCalendar. */
  ics: string
  /** `ics` under the `webcal:` scheme, which is what a calendar app registers
      itself for: an https link opens in the browser, a webcal one subscribes. */
  webcal: string
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

/** One API path (`watch/feed.atom`, `watch/releases.ics`, ...) as an absolute
    URL under the configured origin/base. */
function endpoint(path: string, origin: string, apiBase: string): URL {
  const base = apiBase ? new URL(apiBase, origin).toString() : origin
  return new URL(`${base.replace(/\/$/, '')}/api/v1/${path}`)
}

/** The same URL under the `webcal:` scheme. Only an http(s) URL is rewritten;
    anything else is handed back untouched, because a scheme this does not
    recognise is not one it can safely re-spell. */
export function toWebcal(url: string): string {
  return url.replace(/^https?:/, 'webcal:')
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
 * Build the stateless subscription URLs for a watchlist - Atom, JSON Feed, the
 * calendar, and the calendar again under `webcal:`. The readable CSV
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

  // One endpoint each, and ONE decision for all of them. The URLs differ only
  // in their path, so the decision is measured against the LONGEST - the ics
  // one ("watch/releases.ics" against "watch/feed.atom") - and every URL then
  // carries the same `s` value. Measuring each separately could hand a reader a
  // CSV feed URL beside a compact calendar URL for one watchlist, which reads
  // as two different subscriptions and makes the page's "this is your list"
  // claim false.
  const atom = endpoint('watch/feed.atom', origin, apiBase)
  const json = endpoint('watch/feed.json', origin, apiBase)
  const ics = endpoint('watch/releases.ics', origin, apiBase)
  const series =
    withSeries(ics, csv).length < PLAIN_URL_LIMIT ? csv : await compactSeries(csv)
  const icsURL = withSeries(ics, series)
  return {
    atom: withSeries(atom, series),
    json: withSeries(json, series),
    ics: icsURL,
    webcal: toWebcal(icsURL),
  }
}
