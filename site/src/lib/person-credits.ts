// How a person page states its credit counts. Pure, so the numbers can be
// pinned by tests without rendering the page.
//
// Two units are in play and they are NOT interchangeable: the API pages a
// person's credit LISTS (a narrator has one credit per recording), while the
// page renders WORKS (several recordings of one book collapse to one card). A
// heading counting works beside a note counting credits reads as a
// contradiction - "Narrated 412" over "500 of 1,982" - so every number here
// carries its own unit in the text.

import type { RecordingCredit, WorkCard } from './api'
import { joinNames } from './languages'

/** "1 work" / "412 works" - so a heading count never states a bare number whose
    unit the reader has to infer. */
export function workCount(n: number): string {
  return `${n.toLocaleString()} ${n === 1 ? 'work' : 'works'}`
}

/** The distinct works behind a person's recording credits (narrated or
    translated), in credit order. A person can be credited on several recordings
    of the same work, so the credit list is deduplicated by work id before it
    becomes cards. */
export function creditedWorks(credits: RecordingCredit[] | undefined): WorkCard[] {
  const works: WorkCard[] = []
  const seen = new Set<string>()
  for (const n of credits ?? []) {
    if (n.work && !seen.has(n.work.id)) {
      seen.add(n.work.id)
      works.push(n.work)
    }
  }
  return works
}

/** The "you are seeing a page" line for the authored list, or undefined when the
    whole list arrived. The API caps a person's credit lists (see
    PERSON_PAGE_MAX) because they are unbounded - a corporate credit such as
    "Full Cast" narrates thousands of works. One authored credit is one work, so
    this side speaks in works throughout. */
export function authoredNote(shown: number, total: number | undefined): string | undefined {
  if (typeof total !== 'number' || shown >= total) return undefined
  return `Showing the first ${workCount(shown)} of ${total.toLocaleString()}.`
}

/** The same line for the narrated list, which is paged in CREDITS while the grid
    below it shows WORKS. Both numbers are named, so the smaller work count next
    to the larger credit count cannot read as a mistake. */
export function narrationNote(
  shownCredits: number,
  total: number | undefined,
  works: number
): string | undefined {
  return creditNote('narration', shownCredits, total, works)
}

/** The same line for the translated list (recording translator credits). */
export function translationNote(
  shownCredits: number,
  total: number | undefined,
  works: number
): string | undefined {
  return creditNote('translation', shownCredits, total, works)
}

function creditNote(
  kind: string,
  shownCredits: number,
  total: number | undefined,
  works: number
): string | undefined {
  if (typeof total !== 'number' || shownCredits >= total) return undefined
  return `Showing the first ${shownCredits.toLocaleString()} of ${total.toLocaleString()} ${kind} credits, which cover the ${workCount(works)} below.`
}

/** The roles whose lists a LATER page of a person's recording credits points
    back to, as prose ("written and translated"), or undefined on the first page
    and for a person with no such list. Those lists do not move with the credit
    window, so the first page alone carries them and a later page links there
    instead of repeating them. Narrated titles are not among them here: in the
    page's island they ARE the paged recording credits (the separate Narrated
    list is only a fallback when those cannot load), so there is nothing on the
    first page to point back to. metaserve's fact sheet, which does render an
    "Audiobooks narrated" list on page 1, names narration in its own note. */
export function firstPageRoles(
  page: number,
  totals: { authored_total?: number; translated_total?: number }
): string | undefined {
  if (page <= 1) return undefined
  const roles = [
    (totals.authored_total ?? 0) > 0 ? 'written' : '',
    (totals.translated_total ?? 0) > 0 ? 'translated' : '',
  ].filter(Boolean)
  return roles.length > 0 ? joinNames(roles) : undefined
}
