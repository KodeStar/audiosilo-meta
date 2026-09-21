import { describe, expect, it } from 'vitest'
import { buildFeedURLs, MAX_FEED_SERIES } from './feed-url'
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
})
