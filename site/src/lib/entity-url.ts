// Where a detail island finds the entity it is about.
//
// metaserve serves the three detail shells at PATH routes - /works/{slug},
// /people/{slug}, /series/{slug} - and 301s the legacy query-parameter URLs
// (/work?id=x) onto them. Both spellings therefore reach a page in production,
// and only the legacy one exists under `yarn dev` (the Astro dev server knows
// nothing about the path routes), so the islands read the path FIRST and fall
// back to `?id=`.
//
// Pure and DOM-free on purpose: the island passes window.location.pathname and
// .search in, so the rule is unit-testable without a DOM harness.

/** The three families that have a detail page. */
export type EntityKind = 'work' | 'person' | 'series'

/** The path segment each kind is served under - the API's own namespace
    spelling (works/people/series), which is also what the redirect table and
    metaserve's HTML routes use. Note person -> "people". */
const pathSegment: Record<EntityKind, string> = {
  work: 'works',
  person: 'people',
  series: 'series',
}

/** Decode one path segment, returning null for a malformed escape (a bare '%'
    makes decodeURIComponent throw) rather than letting it reach a fetch. */
function decodeSegment(raw: string): string | null {
  try {
    return decodeURIComponent(raw)
  } catch {
    return null
  }
}

/**
 * The entity slug this location addresses, or null when it addresses none.
 *
 * Reads `/works/{slug}` (the kind's own plural segment) first and the legacy
 * `?id=` second. The path match is STRICT - exactly two segments, both
 * non-empty - so `/works`, `/works/`, `/works/a/b` and `/worksy/a` all fall
 * through to the query string, which is what keeps a future `/works/a/reviews`
 * from being read as the work `a`.
 *
 * An `?id=` that is present but empty yields '' (not null), so a caller can
 * tell "no id in this URL" from "an id was supplied and it was blank" - the
 * distinction useEntity turns into its not-found state.
 */
export function entitySlugFromLocation(
  pathname: string,
  search: string,
  kind: EntityKind
): string | null {
  const segments = pathname.split('/')
  // A leading '/' makes segments[0] the empty string, so an exact path match is
  // ['', '<segment>', '<slug>'] and nothing else.
  if (segments.length === 3 && segments[0] === '' && segments[1] === pathSegment[kind]) {
    const slug = segments[2] ? decodeSegment(segments[2]) : null
    if (slug) return slug
  }
  return new URLSearchParams(search).get('id')
}
