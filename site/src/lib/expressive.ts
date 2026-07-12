// Pure presentation helpers for the community-authored expressive layer
// (characters + recaps) shown on a work page. Kept framework-free so they can be
// unit-tested; the React components in WorkDetail.tsx consume them.
import type { Character, Recap } from './api'

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
