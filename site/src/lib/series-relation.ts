// What a series IS, in words a results list can show. Several series can share
// one name - "Harry Potter" is the English series AND its German and Spanish
// editions, each named exactly that - and a list of identical names is one a
// reader cannot choose from. The API states what tells them apart (a series
// search hit's translation_of and, on a reading-order variant, ordering /
// ordering_of / ordering_of_name), and this file turns those facts into the
// row's secondary line. The language chip (lib/languages.ts needsBadge) says
// WHICH language; this says what the series is relative to the others.
//
// Pure and DOM-free, so the wording is pinned by series-relation.test.ts rather
// than by eye.

import type { SeriesOrdering, SeriesTranslation } from './api'
import { joinNames } from './languages'

/** How a reading order reads as the head of a phrase ("Chronological order of
    Harry Potter"). Keyed by the ordering enum, so a value the schema gains is a
    type error here until it is worded. */
export const ORDERING_LABELS: Record<SeriesOrdering, string> = {
  publication: 'Publication order',
  chronological: 'Chronological order',
  recommended: 'Recommended order',
}

/** The facts a series row is described from - a subset of a series search hit,
    every one optional because an older metaserve sends none of them. */
export interface SeriesFacts {
  works?: number
  ordering?: string
  ordering_of?: string
  ordering_of_name?: string
  translation_of?: SeriesTranslation[]
}

function orderingLabel(ordering: string | undefined): string {
  return ordering && Object.hasOwn(ORDERING_LABELS, ordering)
    ? ORDERING_LABELS[ordering as SeriesOrdering]
    : 'Reading order'
}

/** What the series is relative to the series it derives from, or null for a
    series that derives from none (the original, which is what an unlabelled row
    then reads as):

    - a reading-order VARIANT: "Chronological order of Harry Potter" - named by
      its primary's name, falling back to the primary's slug when the name is
      absent, and to "Reading order" when the variant states no ordering;
    - a TRANSLATION: "Translation of Harry Potter" (several originals are joined:
      "Translation of A and B").

    A variant is described as a variant whatever else it states, matching the
    server's ranking, which places a variant after the translations. */
export function seriesRelation(s: SeriesFacts): string | null {
  if (s.ordering_of) {
    return `${orderingLabel(s.ordering)} of ${s.ordering_of_name || s.ordering_of}`
  }
  const originals = (s.translation_of ?? []).map((t) => t.name || t.id).filter(Boolean)
  if (originals.length > 0) return `Translation of ${joinNames(originals)}`
  return null
}

/** A series row's whole secondary line: its relation, then its member count -
    "Translation of Harry Potter · 7 works" - or null when there is neither. */
export function seriesSubline(s: SeriesFacts): string | null {
  const parts: string[] = []
  const relation = seriesRelation(s)
  if (relation) parts.push(relation)
  if (s.works != null) parts.push(`${s.works} ${s.works === 1 ? 'work' : 'works'}`)
  return parts.length > 0 ? parts.join(' · ') : null
}
