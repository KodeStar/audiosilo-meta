// Pure presentation helpers for the community-authored expressive layer
// (characters + recaps), shown on a work page and on the two community-guide
// pages that hang off it. Kept framework-free so they can be unit-tested; the
// React components in WorkDetail.tsx and work-guide.tsx consume them, and the
// presence rules live here rather than in either so the work page's tabs and the
// guide page's empty state cannot disagree about what a work carries.
import type { Character, Recap, RecapSummary, Work } from './api'
import type { WorkGuide } from './entity-url'

/** The Creative Commons deed the community layer (characters + recaps) is
    published under, as opposed to the CC0 catalogue everything else here is.
    Stated once so every surface that has to name the terms - the guide pages'
    footer, and anything that grows one later - links the same deed.
    internal/serve carries its own copy for the server-rendered pages; see
    LICENSING.md for what the two layers mean. */
export const CC_BY_SA_URL = 'https://creativecommons.org/licenses/by-sa/4.0/'

/** Human label for a character role, or null when the role is absent/unknown. */
export function roleLabel(role: Character['role']): string | null {
  switch (role) {
    case 'protagonist':
      return 'Protagonist'
    case 'antagonist':
      return 'Antagonist'
    case 'supporting':
      return 'Supporting'
    case 'minor':
      return 'Minor'
    default:
      return null
  }
}

/** Where a character first appears, phrased for a reader.
    Chapter 0 (or 1) reads as "from the start"; later chapters name the chapter. */
export function revealLabel(reveal: Character['reveal']): string {
  const ch = reveal.chapter
  if (ch <= 1) return 'From the start'
  return `From chapter ${ch}`
}

/** Heading for a recap, keyed on its position and scope. A chapter-0 "series"
    recap is the "previously, in earlier books" catch-up; everything else is a
    within-book "story so far up to chapter N". */
export function recapLabel(recap: Recap): string {
  const ch = recap.through.chapter
  if (ch === 0 && recap.scope === 'series') return 'Previously, in earlier books'
  if (ch === 0) return 'Before this book'
  return `Up to chapter ${ch}`
}

/** Short scope tag for a recap, or null when scope is absent. */
export function scopeLabel(scope: Recap['scope']): string | null {
  switch (scope) {
    case 'series':
      return 'earlier books'
    case 'book':
      return 'this book'
    default:
      return null
  }
}

/** Recaps sorted by position (ascending), so the "story so far" reads in order.
    The API already returns them ordered; this makes the component independent of
    that guarantee. Returns a new array; does not mutate the input. */
export function sortRecaps(recaps: Recap[]): Recap[] {
  return [...recaps].sort((a, b) => a.through.chapter - b.through.chapter)
}

/** One row in the "Story so far" list, ready to render: a header title, an
    optional scope/kind badge, and the spoiler text revealed on open. wholeBook
    marks the full-spoiler summary rows (In short / How did it end?), as opposed
    to the position-bounded chaptered recaps. */
export interface StoryRow {
  title: string
  badge?: string
  text: string
  wholeBook?: boolean
}

/** The ordered "Story so far" rows for a work: the whole-book "In short" row
    first, then the chaptered recaps by position, then the whole-book
    "How did it end?" row last. A summary row exists only when its text is present
    (an empty string counts as absent). This is the single source for the row set -
    the panel maps it, and the tab's count and presence (empty = no tab, so a work
    carrying only a whole-book summary still gets the tab) derive from its length. */
export function storyRows(recaps: Recap[], summary?: RecapSummary): StoryRow[] {
  const rows: StoryRow[] = []
  if (summary?.in_short) {
    rows.push({
      title: 'In short',
      badge: 'whole book',
      text: summary.in_short,
      wholeBook: true,
    })
  }
  for (const r of sortRecaps(recaps)) {
    rows.push({ title: recapLabel(r), badge: scopeLabel(r.scope) ?? undefined, text: r.text })
  }
  if (summary?.ending) {
    rows.push({
      title: 'How did it end?',
      badge: 'ending',
      text: summary.ending,
      wholeBook: true,
    })
  }
  return rows
}

/** storyRows for a whole work, with the two optional fields unwrapped. It is the
    ONE spelling of that incantation: the work page's tab and CTA and the recap
    guide page all ask the same question of the same two fields, and a caller
    that spelled `work.recaps ?? []` differently would be asking a different one. */
export function storyRowsOf(work: Work): StoryRow[] {
  return storyRows(work.recaps ?? [], work.recap_summary)
}

/** Does this work carry the member a given guide page is about? The ONE
    predicate behind every question that turns on it: whether the guide page
    renders its panel or its empty state, whether the sibling guide exists to
    link to, and which contribution CTAs the work page offers.

    Recap presence reads off storyRows rather than off `recaps` alone, so a work
    carrying only a whole-book summary counts as having a recap - exactly as
    metaserve's hasRecapGuide does, which is what keeps the page's own existence
    and the site's link to it from disagreeing.

    `rows` is that row set, for a caller that has ALREADY built it to render it
    (the recap guide page). Passing it in is what lets the presence rule stay one
    definition without the row set being composed twice per render; everyone else
    omits it and this builds its own. */
export function hasGuide(work: Work, guide: WorkGuide, rows?: StoryRow[]): boolean {
  return guide === 'characters'
    ? (work.characters?.length ?? 0) > 0
    : (rows ?? storyRowsOf(work)).length > 0
}
