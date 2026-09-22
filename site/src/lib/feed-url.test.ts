import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildFeedURLs, MAX_FEED_SERIES, toWebcal } from './feed-url'
import { WATCHLIST_VERSION, type Watchlist } from './watchlist'

function watchlist(slugs: string[], hidden: string[] = []): Watchlist {
  return {
    version: WATCHLIST_VERSION,
    series: Object.fromEntries(
      slugs.map((slug) => [
        slug,
        {
          name: slug,
          watchedAt: '2026-09-21',
          owned: [],
          seen: [],
          skipped: [],
          ...(hidden.includes(slug) ? { hidden: true as const } : {}),
        },
      ])
    ),
  }
}

describe('buildFeedURLs', () => {
  it('uses readable CSV for a short list and omits hidden series', async () => {
    const urls = await buildFeedURLs(
      watchlist(['beta-series', 'hidden-series', 'alpha-series'], ['hidden-series']),
      'https://meta.example',
      ''
    )
    expect(urls.atom).toBe(
      'https://meta.example/api/v1/watch/feed.atom?s=alpha-series%2Cbeta-series'
    )
    expect(urls.json).toBe(
      'https://meta.example/api/v1/watch/feed.json?s=alpha-series%2Cbeta-series'
    )
    expect(urls.ics).toBe(
      'https://meta.example/api/v1/watch/releases.ics?s=alpha-series%2Cbeta-series'
    )
    expect(urls.webcal).toBe(
      'webcal://meta.example/api/v1/watch/releases.ics?s=alpha-series%2Cbeta-series'
    )
  })

  it('respects a configured API base', async () => {
    const urls = await buildFeedURLs(
      watchlist(['alpha-series']),
      'https://meta.example',
      'https://api.example/base/'
    )
    expect(urls.atom).toBe(
      'https://api.example/base/api/v1/watch/feed.atom?s=alpha-series'
    )
    expect(urls.ics).toBe(
      'https://api.example/base/api/v1/watch/releases.ics?s=alpha-series'
    )
  })

  it('refuses a list the server would reject, saying why', async () => {
    // The server's cap (maxWatchSeries, internal/serve/seriesparam.go) - a URL
    // past it 400s inside the reader's feed reader, where nobody would see it.
    const slugs = Array.from({ length: MAX_FEED_SERIES + 1 }, (_, i) => `series-${i}`)
    await expect(buildFeedURLs(watchlist(slugs), 'https://meta.example', '')).rejects.toThrow(
      /201/
    )
    // Exactly at the cap still builds.
    await expect(
      buildFeedURLs(watchlist(slugs.slice(0, MAX_FEED_SERIES)), 'https://meta.example', '')
    ).resolves.toBeTruthy()
  })

  it('refuses a watchlist with nothing visible', async () => {
    await expect(
      buildFeedURLs(watchlist(['only-series'], ['only-series']), 'https://meta.example', '')
    ).rejects.toThrow()
  })

  it('uses raw DEFLATE for a long URL and round-trips the CSV', async () => {
    const slugs = Array.from({ length: 40 }, (_, i) =>
      `series-${String(i).padStart(3, '0')}-${'long-name-'.repeat(8)}end`
    )
    const urls = await buildFeedURLs(watchlist(slugs), 'https://meta.example', '')
    const value = new URL(urls.atom).searchParams.get('s') ?? ''
    expect(value.startsWith('z:')).toBe(true)
    expect(value).not.toContain('=')

    const encoded = value.slice(2).replace(/-/g, '+').replace(/_/g, '/')
    const padded = encoded + '='.repeat((4 - (encoded.length % 4)) % 4)
    const compressed = Uint8Array.from(atob(padded), (char) => char.charCodeAt(0))
    const output = new Blob([compressed])
      .stream()
      .pipeThrough(new DecompressionStream('deflate-raw'))
    const decoded = await new Response(output).text()
    expect(decoded).toBe([...slugs].sort().join(','))
  })

  it('gives all four URLs the same s value in the compact form', async () => {
    const slugs = Array.from({ length: 40 }, (_, i) =>
      `series-${String(i).padStart(3, '0')}-${'long-name-'.repeat(8)}end`
    )
    const urls = await buildFeedURLs(watchlist(slugs), 'https://meta.example', '')
    const value = (url: string) => new URL(url).searchParams.get('s') ?? ''
    expect(value(urls.atom).startsWith('z:')).toBe(true)
    expect(value(urls.json)).toBe(value(urls.atom))
    expect(value(urls.ics)).toBe(value(urls.atom))
    expect(urls.webcal).toBe(toWebcal(urls.ics))
  })

  it('decides CSV-or-compact on the ICS URL, the longest of the three', async () => {
    // "watch/releases.ics" is three characters longer than "watch/feed.atom",
    // so a watchlist exists whose Atom URL fits under the 1500-character limit
    // and whose ICS URL does not. One decision, measured on the longest, keeps
    // every URL carrying the same `s` - this list is where measuring the Atom
    // one instead would have split them.
    const slugs = Array.from({ length: 28 }, (_, i) =>
      `series-${String(i).padStart(2, '0')}-${'x'.repeat(39)}`
    )
    const encoded = encodeURIComponent([...slugs].sort().join(','))
    const atomWithCSV = `https://meta.example/api/v1/watch/feed.atom?s=${encoded}`
    const icsWithCSV = `https://meta.example/api/v1/watch/releases.ics?s=${encoded}`
    expect(atomWithCSV.length).toBeLessThan(1500)
    expect(icsWithCSV.length).toBeGreaterThanOrEqual(1500)

    const urls = await buildFeedURLs(watchlist(slugs), 'https://meta.example', '')
    for (const url of [urls.atom, urls.json, urls.ics]) {
      expect(new URL(url).searchParams.get('s')?.startsWith('z:')).toBe(true)
    }
  })

  // The compact form is a cosmetic shortening, not a contract: the server takes
  // a plain CSV of the same 200 series (validateSeriesList,
  // internal/serve/seriesparam.go), so a browser without CompressionStream gets
  // a working feed rather than an error about a shortening it never asked for.
  it('falls back to the plain CSV where the browser cannot compress', async () => {
    vi.stubGlobal('CompressionStream', undefined)
    const slugs = Array.from({ length: 40 }, (_, i) =>
      `series-${String(i).padStart(3, '0')}-${'long-name-'.repeat(8)}end`
    )
    const urls = await buildFeedURLs(watchlist(slugs), 'https://meta.example', '')
    const value = new URL(urls.atom).searchParams.get('s') ?? ''
    expect(value).toBe([...slugs].sort().join(','))
    // Still one decision for all four URLs.
    for (const url of [urls.json, urls.ics]) {
      expect(new URL(url).searchParams.get('s')).toBe(value)
    }
    expect(urls.webcal).toBe(toWebcal(urls.ics))
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('toWebcal', () => {
  it('rewrites an https or http URL and leaves anything else alone', () => {
    expect(toWebcal('https://meta.example/api/v1/watch/releases.ics?s=a')).toBe(
      'webcal://meta.example/api/v1/watch/releases.ics?s=a'
    )
    expect(toWebcal('http://localhost:4321/api/v1/watch/releases.ics')).toBe(
      'webcal://localhost:4321/api/v1/watch/releases.ics'
    )
    expect(toWebcal('/api/v1/watch/releases.ics')).toBe('/api/v1/watch/releases.ics')
    expect(toWebcal('webcal://meta.example/x.ics')).toBe('webcal://meta.example/x.ics')
    // Only a LEADING scheme is rewritten - an https inside a query value is data.
    expect(toWebcal('ftp://x/https:y')).toBe('ftp://x/https:y')
  })
})
