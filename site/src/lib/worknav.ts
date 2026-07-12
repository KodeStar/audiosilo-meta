// Pure navigation helpers for the work page: flipping through a series' volumes
// in order, and mapping the active tab to a URL hash so the choice survives
// navigating between works. Kept framework-free so they can be unit-tested; the
// React components in WorkDetail.tsx consume them.
import type { Series } from './api'

/** One entry in a series' ordered work list ({ position, work }). */
export type SeriesWork = Series['works'][number]

/** The work-page tabs. General is the default (no hash); characters/recaps only
    exist when the work carries that sidecar. */
export type WorkTab = 'general' | 'characters' | 'recaps'

/** The previous and next entries around a work within a series (or null at the
    ends / when the work is absent). */
export interface SeriesNeighbors {
  prev: SeriesWork | null
  next: SeriesWork | null
}

/** Numeric start of a series position string, mirroring the server's ordering
    (internal/serve positionStart): "2.5" -> 2.5, "1-3.5" -> 1, unparseable sorts
    last. This keeps the neighbour lookup independent of the API's ordering. */
export function positionStart(pos: string): number {
  let p = pos.trim()
  const dash = p.indexOf('-')
  if (dash > 0) p = p.slice(0, dash)
  const n = Number.parseFloat(p.trim())
  return Number.isNaN(n) ? Number.POSITIVE_INFINITY : n
}

/** The entries immediately before and after currentWorkId within a series'
    member list, ordered by position (ascending, the same rule the series page
    and server use). Returns nulls when the current work is not in the list, and
    a one-sided result at the first/last volume. Returns a new sort; does not
    mutate the input. */
export function seriesNeighbors(works: SeriesWork[], currentWorkId: string): SeriesNeighbors {
  const ordered = [...works].sort((a, b) => positionStart(a.position) - positionStart(b.position))
  const idx = ordered.findIndex((entry) => entry.work.id === currentWorkId)
  if (idx < 0) return { prev: null, next: null }
  return {
    prev: idx > 0 ? ordered[idx - 1] : null,
    next: idx < ordered.length - 1 ? ordered[idx + 1] : null,
  }
}

/** The URL hash fragment for a tab: "" for General (so the fragment is cleared),
    "#characters" and "#story-so-far" for the sidecar tabs. */
export function hashForTab(tab: WorkTab): string {
  switch (tab) {
    case 'characters':
      return '#characters'
    case 'recaps':
      return '#story-so-far'
    default:
      return ''
  }
}

/** The active tab implied by a location hash, falling back to General when the
    hash is absent/unknown or names a tab this work does not have (e.g. a
    #story-so-far deep link onto a work with no recaps). */
export function tabFromHash(
  hash: string,
  avail: { hasCharacters: boolean; hasRecaps: boolean }
): WorkTab {
  const frag = hash.replace(/^#/, '')
  if (frag === 'characters' && avail.hasCharacters) return 'characters'
  if (frag === 'story-so-far' && avail.hasRecaps) return 'recaps'
  return 'general'
}
