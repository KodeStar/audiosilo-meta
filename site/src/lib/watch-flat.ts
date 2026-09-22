// The watching page's cross-series view: one flat "Available" list and one flat
// "Preorder" list, pooled from every watched series and sorted by DATE rather
// than by series.
//
// The per-series panels answer "where am I in this series"; this answers the
// other question a reader actually opens the page with - "what came out, and
// what is coming" - which no amount of scrolling through position-ordered
// panels does. Pure and framework-free like the rest of lib/: the panels are
// handed in already classified (lib/watchlist.ts), so nothing here fetches,
// stores or re-derives what a reader owns.

import type { SeriesEntry } from './api'
import type { ClassifiedEntry, SeriesClassification } from './watchlist'

/** One entry lifted out of its series panel, carrying the series it came from
    so the flat list can still say where each book belongs. */
export interface FlatEntry {
  /** The SERIES slug - what the entry's panel is keyed by, and what a link back
      to the series page needs. The work's own slug is `entry.work.id`. */
  slug: string
  /** The series NAME, as the panel renders it. */
  series: string
  entry: SeriesEntry
  /** Carried through from the classification: the work has not been recorded as
      seen yet, so it still earns a "New" badge in the flat list too. */
  isNew: boolean
}

export interface FlatLists {
  preorder: FlatEntry[]
  available: FlatEntry[]
}

/** The panel shape the watching page already holds, narrowed to what this file
    reads. */
interface Panel {
  slug: string
  name: string
  result: SeriesClassification
}

function lift(panel: Panel, classified: ClassifiedEntry): FlatEntry {
  return {
    slug: panel.slug,
    // A watch stores the series name it was made under, but a record written by
    // an older build (or a hand-edited backup) can carry an empty one, and a
    // blank heading reads as a bug. The slug is always there.
    series: panel.name || panel.slug,
    entry: classified.entry,
    isNew: classified.isNew,
  }
}

/**
 * Pool every panel's preorder and available entries into two flat lists, each
 * returned ALREADY sorted by the comparator below - the caller renders what it
 * is handed, so there is no second ordering seam for a component to get wrong.
 *
 * Each WORK appears ONCE, under the first panel (input order) that lists it.
 * The per-series panels are the place a book is shown once per series it
 * belongs to; this list answers "what came out", and one book listed twice is
 * one book the reader ticks off twice. The same rule the server's feed applies
 * (internal/serve/watchfeed.go, `seenWork`), for the same reason. It is one set
 * across BOTH lists: an entry cannot be a preorder in one series and available
 * in another, since the classification reads the work's own release date.
 */
export function flattenAcrossSeries(panels: readonly Panel[]): FlatLists {
  const preorder: FlatEntry[] = []
  const available: FlatEntry[] = []
  const seen = new Set<string>()
  for (const panel of panels) {
    for (const classified of panel.result.preorder) {
      if (seen.has(classified.entry.work.id)) continue
      seen.add(classified.entry.work.id)
      preorder.push(lift(panel, classified))
    }
    for (const classified of panel.result.available) {
      if (seen.has(classified.entry.work.id)) continue
      seen.add(classified.entry.work.id)
      available.push(lift(panel, classified))
    }
  }
  preorder.sort(comparePreorder)
  available.sort(compareAvailable)
  return { preorder, available }
}

/** The series name then the position, the tie-break both comparators share: two
    books out on one day read best grouped by series, in series order. */
function byNameThenPosition(a: FlatEntry, b: FlatEntry): number {
  const name = a.series.localeCompare(b.series)
  if (name !== 0) return name
  // Compared rather than subtracted: two unparseable positions are both
  // +Infinity and the difference of those is NaN, which is not an ordering.
  const pa = positionStart(a.entry.position)
  const pb = positionStart(b.entry.position)
  if (pa !== pb) return pa < pb ? -1 : 1
  return a.entry.position.localeCompare(b.entry.position)
}

/**
 * The one date comparator, in whichever direction the caller wants.
 *
 * Dates compare as PLAIN STRINGS. The catalogue states a release date at
 * whatever precision its source gave (`YYYY`, `YYYY-MM` or `YYYY-MM-DD`), and
 * string order sorts those chronologically while letting the less precise value
 * win a tie - the same rule the server picks a card's date by (`workCard` in
 * internal/serve/store.go). Parsing would drag the reader's timezone into a
 * fact that has none, exactly as lib/dates.ts says.
 *
 * An entry with NO date sorts LAST in BOTH directions: most of them are old
 * books nobody recorded a date for, so treating an absent date as infinitely
 * old would be a guess and floating it to the top would bury the releases the
 * list exists to surface.
 */
function byDate(a: FlatEntry, b: FlatEntry, newestFirst: boolean): number {
  const da = a.entry.work.release_date ?? ''
  const db = b.entry.work.release_date ?? ''
  if (!da !== !db) return da ? -1 : 1
  if (da !== db) return (da < db) === newestFirst ? 1 : -1
  return byNameThenPosition(a, b)
}

/** Preorders, soonest first. Every preorder has a date by construction (an
    entry with none is classified `available`), so the missing-date arm above
    never fires here. */
export function comparePreorder(a: FlatEntry, b: FlatEntry): number {
  return byDate(a, b, false)
}

/** Available entries, newest first - what a reader wants at the top of "out
    now" is what just came out. */
export function compareAvailable(a: FlatEntry, b: FlatEntry): number {
  return byDate(a, b, true)
}

/**
 * The numeric start of a series position: "2.5" -> 2.5, "1-3" -> 1, "03" -> 3.
 * An unparseable value yields +Infinity so it sorts last rather than silently
 * leading the list as a zero.
 *
 * HAND-MIRRORED TWIN of Go `positionStart` in internal/serve/queries.go, which
 * sorts the same position strings server-side and is the RULE OF RECORD (it
 * orders the series rail every consumer reads). The sentinel differs in
 * spelling only (Go has no +Inf literal in that expression and uses 1e18). Both
 * sides pin the SAME cases - Go's TestPositionStart and the `positionStart`
 * block in watch-flat.test.ts - because a series rail and this list ordering
 * one series two ways is exactly the disagreement a single rule avoids.
 */
export function positionStart(position: string): number {
  let head = position.trim()
  const dash = head.indexOf('-', 1)
  if (dash > 0) head = head.slice(0, dash).trim()
  // Number('') is 0 and Number(' 1 ') is 1, so the emptiness check is explicit
  // and the value is trimmed before it is read.
  if (!head) return Number.POSITIVE_INFINITY
  const value = Number(head)
  return Number.isFinite(value) ? value : Number.POSITIVE_INFINITY
}
