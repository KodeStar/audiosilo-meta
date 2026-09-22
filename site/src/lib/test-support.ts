// Shared fixtures for the lib/ unit tests. Not a test file itself (vitest.config.ts
// includes `src/**/*.test.ts`), so it is only ever imported.

import { vi } from 'vitest'
import type { SeriesEntry } from './api'

/** A minimal in-memory localStorage, stubbed onto the global. The returned map
    is the backing store, so a test can assert what was written. `throws: true`
    stands in for a browser with storage blocked. */
export function stubStorage(opts: { throws?: boolean } = {}): Map<string, string> {
  const map = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => {
      if (opts.throws) throw new Error('blocked')
      return map.get(k) ?? null
    },
    setItem: (k: string, v: string) => {
      if (opts.throws) throw new Error('blocked')
      map.set(k, v)
    },
  })
  return map
}

/** One series entry, titled after its own slug so an assertion reads as the id
    it is about. */
export function entry(id: string, position: string, release_date?: string): SeriesEntry {
  return {
    position,
    work: { id, title: id, authors: [], release_date },
  }
}
