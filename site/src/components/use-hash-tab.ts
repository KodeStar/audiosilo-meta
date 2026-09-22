// The tab-bar-in-the-URL-hash rule, shared by the work page and the watching
// page: a reader can link to (or reload into) the view they were on, and the
// tab they are on is what the address bar says.
//
// Each page keeps its OWN mapping (lib/worknav.ts, lib/watchnav.ts) - the work
// page's depends on which sidecars a work carries and the watching page's does
// not - so what is shared is the mechanism, not the vocabulary.

import { useCallback, useEffect, useRef, useState } from 'react'

/** Rewrite the fragment without touching the scroll position or the history
    stack. replaceState fires NO hashchange event, which is what keeps the
    listener below from looping back into a tab this hook just set. */
function replaceHash(hash: string): void {
  window.history.replaceState(
    null,
    '',
    `${window.location.pathname}${window.location.search}${hash}`
  )
}

/**
 * The active tab and a setter that keeps the URL hash in step.
 *
 * Both callers are client:only islands, so `window` is there at first render
 * and there is no SSR pass to agree with: the initial tab is read from the hash
 * directly. A fragment the mapping does not recognise falls back to the default
 * tab, and the stale fragment is then dropped on mount - copying a URL onward
 * that names a tab nobody landed on would mislead.
 *
 * A `hashchange` (an in-page link back to `#notify`, or the reader's back
 * button) sets the tab too, and CANONICALISES: a fragment the mapping does not
 * recognise falls back to the default tab, exactly as on mount, so the address
 * bar never keeps naming a tab nobody landed on. Both `fromHash` and `toHash`
 * are held in refs because they close over state that can change - the work
 * page's tab set depends on which sidecars loaded - and the listener must
 * always read the LATEST rule without being torn down and rebuilt on every
 * render.
 */
export function useHashTab<T extends string>(
  fromHash: (hash: string) => T,
  toHash: (tab: T) => string
): [T, (next: T) => void] {
  const read = useRef(fromHash)
  read.current = fromHash
  const write = useRef(toHash)
  write.current = toHash

  const [tab, setTab] = useState<T>(() => fromHash(window.location.hash))

  useEffect(() => {
    const canonical = toHash(tab)
    if (window.location.hash !== canonical) replaceHash(canonical)
    // Mount only: from here on selectTab owns the hash.
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onHashChange = () => {
      const next = read.current(window.location.hash)
      setTab(next)
      // replaceHash fires no hashchange, so this cannot re-enter.
      const canonical = write.current(next)
      if (window.location.hash !== canonical) replaceHash(canonical)
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const selectTab = useCallback(
    (next: T) => {
      setTab(next)
      replaceHash(toHash(next))
    },
    [toHash]
  )

  return [tab, selectTab]
}
