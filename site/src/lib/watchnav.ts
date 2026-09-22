// Pure navigation helpers for the watching page: mapping its active tab to a
// URL hash, so a reader can link to (or reload into) the view they were on.
//
// Deliberately NOT a generalisation of lib/worknav.ts's tab pair. That one
// gates on AVAILABILITY - a #story-so-far deep link onto a work with no recaps
// falls back to General - and this page has no such question: all four tabs
// exist for every reader, including one watching nothing. Folding the two
// together would mean carrying an availability argument that is always true.

/** The watching-page tabs. Available is the default, so it owns the empty
    hash - a reader arriving with no fragment lands on the list of books they
    can actually buy today. */
export type WatchTab = 'available' | 'all' | 'notify' | 'import'

/** The URL hash fragment for a tab: "" for Available (so the fragment is
    cleared), "#<tab>" for the rest. */
export function hashForWatchTab(tab: WatchTab): string {
  switch (tab) {
    case 'all':
      return '#all'
    case 'notify':
      return '#notify'
    case 'import':
      return '#import'
    default:
      return ''
  }
}

/** The active tab implied by a location hash, falling back to Available when
    the hash is absent or names something this page does not have (an old
    bookmark, a heading anchor someone linked). Tolerates a fragment written
    without its leading "#". */
export function watchTabFromHash(hash: string): WatchTab {
  const frag = hash.replace(/^#/, '')
  if (frag === 'all' || frag === 'notify' || frag === 'import') return frag
  return 'available'
}
