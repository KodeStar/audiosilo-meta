// The one React binding over lib/watchlist.ts: the store as island state, plus
// a save that persists whatever a (pure) mutation handed back.
//
// It is deliberately thin. Every rule about what a watchlist IS lives in the
// library, which is framework-free and tested; this hook exists so the series
// page and the watching page cannot end up with two different ideas of when to
// write to localStorage.
//
// Reading in useState's initializer is safe because both islands are
// `client:only="react"` - there is no server render whose markup this could
// disagree with, exactly as WorkDetail's marketplace pick already relies on.

import { useCallback, useState } from 'react'
import {
  WATCHLIST_CHANGED_EVENT,
  readWatchlist,
  writeWatchlist,
  type Watchlist,
} from '../../lib/watchlist'

export interface WatchlistHandle {
  store: Watchlist
  /** Persist and re-render. Callers pass the result of a lib/watchlist
      mutation - `save(setOwned(store, slug, work, true))` - so the store the
      island renders and the store on disk can never diverge. */
  save: (next: Watchlist) => void
}

export function useWatchlist(): WatchlistHandle {
  const [store, setStore] = useState<Watchlist>(readWatchlist)
  const save = useCallback((next: Watchlist) => {
    writeWatchlist(next)
    // The header's "Watching" badge lives outside every island - it is plain DOM
    // in Header.astro's script - so React's re-render never reaches it. It
    // recounts on this event, which is why a mark made here drops the badge
    // immediately instead of waiting for the next page load. The `storage`
    // event covers the other-tab case; it deliberately does not fire in the tab
    // that wrote, which is exactly the gap this fills.
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent(WATCHLIST_CHANGED_EVENT))
    }
    setStore(next)
  }, [])
  return { store, save }
}
