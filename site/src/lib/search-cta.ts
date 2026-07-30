// A tiny, self-contained URL builder for the search empty-state "add it" CTA.
//
// It is deliberately kept LOCAL to the search feature rather than added to
// github-prefill.ts: the prefill builders there take a fully-parsed ParsedBook,
// whereas the search CTA only ever has the raw query string a user typed. Pure,
// React/DOM-free, so it is unit-tested directly. The new-issue base is shared
// with github-prefill.ts rather than duplicated.

import { ISSUE_BASE } from './github-prefill'

/**
 * Build a prefilled add-work.yml issue URL from a raw search query, seeding the
 * work Title field (`work_title`) and the issue title with the query. The issue
 * title reuses the template's `[work] ` prefix so the tracker stays consistent
 * with the other add-work hand-offs (github-prefill.addWorkIssueUrl).
 *
 * The query is trimmed; an empty query yields the unprefilled form URL.
 */
export function addWorkFromQueryUrl(query: string): string {
  const title = query.trim()
  const p = new URLSearchParams()
  p.set('template', 'add-work.yml')
  if (title) {
    p.set('title', `[work] ${title}`)
    p.set('work_title', title)
  }
  return `${ISSUE_BASE}?${p.toString()}`
}

/** The lookup-assist page, which searches libex (the public Audible metadata
    mirror) so the reader can pick the right edition instead of typing every
    field by hand. */
const ADD_PATH = '/add'

/**
 * The query-string parameter /add reads its opening term from. This module
 * WRITES it and the /add island READS it, so it lives here as one exported
 * constant - renaming it on one side alone would silently break the funnel
 * (the page would just sit idle) with nothing to catch it.
 */
export const ADD_QUERY_PARAM = 'q'

/**
 * Link the search empty state to the /add lookup assist, carrying the query the
 * reader already typed so the page can act on it immediately. An empty query
 * yields the bare page.
 *
 * One param covers both cases: a pasted product code arrives here as the query
 * too, and /add resolves an ASIN-shaped `q` straight to that record instead of
 * searching for it - so there is no separate ASIN route to keep in step.
 */
export function addFromLibexUrl(query: string): string {
  const q = query.trim()
  return q ? `${ADD_PATH}?${ADD_QUERY_PARAM}=${encodeURIComponent(q)}` : ADD_PATH
}
