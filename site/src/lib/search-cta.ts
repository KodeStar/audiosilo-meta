// A tiny, self-contained URL builder for the search empty-state "add it" CTA.
//
// It is deliberately kept LOCAL to the search feature rather than added to
// github-prefill.ts: the prefill builders there take a fully-parsed ParsedBook,
// whereas the search CTA only ever has the raw query string a user typed. Pure,
// React/DOM-free, so it is unit-tested directly.

const ADD_WORK_ISSUE_BASE = 'https://github.com/kodestar/audiosilo-meta/issues/new'

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
  return `${ADD_WORK_ISSUE_BASE}?${p.toString()}`
}
