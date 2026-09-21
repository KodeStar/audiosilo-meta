import { API_BASE } from './api'
import type { Watchlist } from './watchlist'

export type FeedFormat = 'atom' | 'json'

export interface FeedURLs {
  atom: string
  json: string
}

const PLAIN_URL_LIMIT = 1500

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

function urlWithSeries(format: FeedFormat, origin: string, apiBase: string, series: string): string {
  const url = endpoint(format, origin, apiBase)
  url.searchParams.set('s', series)
  return url.toString()
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
 */
export async function buildFeedURLs(
  store: Watchlist,
  origin = globalThis.location.origin,
  apiBase = API_BASE
): Promise<FeedURLs> {
  const csv = watchedSlugs(store).join(',')
  if (!csv) throw new Error('at least one visible series is required')

  const plainAtom = urlWithSeries('atom', origin, apiBase, csv)
  const series = plainAtom.length < PLAIN_URL_LIMIT ? csv : await compactSeries(csv)
  return {
    atom: urlWithSeries('atom', origin, apiBase, series),
    json: urlWithSeries('json', origin, apiBase, series),
  }
}
