// Pure navigation helpers for the watching page: mapping its active tab to a
// URL hash, so a reader can link to (or reload into) the view they were on.
//
// Deliberately NOT a generalisation of lib/worknav.ts's tab pair. That one
// gates on AVAILABILITY - a #story-so-far deep link onto a work with no recaps
// falls back to General - and this page has no such question: all four tabs
// exist for every reader, including one watching nothing. Folding the two
// together would mean carrying an availability argument that is always true.

/** The watching-page tabs, in the order the tab bar renders them and the one
    place they are listed - the type, the hash rule and the bar all read this.
    Available is first and is the default, so it owns the empty hash: a reader
    arriving with no fragment lands on the list of books they can buy today. */
export const WATCH_TABS = ['available', 'all', 'notify', 'import'] as const

export type WatchTab = (typeof WATCH_TABS)[number]

/** The URL hash fragment for a tab: "" for Available (so the fragment is
    cleared), "#<tab>" for the rest. */
export function hashForWatchTab(tab: WatchTab): string {
  return tab === 'available' ? '' : '#' + tab
}

/** The active tab implied by a location hash, falling back to Available when
    the hash is absent or names something this page does not have (an old
    bookmark, a heading anchor someone linked). Tolerates a fragment written
    without its leading "#". `#available` resolves to Available too, though
    hashForWatchTab spells that tab as the empty fragment. */
export function watchTabFromHash(hash: string): WatchTab {
  const frag = hash.replace(/^#/, '')
  return (WATCH_TABS as readonly string[]).includes(frag) ? (frag as WatchTab) : 'available'
}
