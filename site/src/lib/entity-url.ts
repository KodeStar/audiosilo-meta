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
 * The slug a location addresses, or null when it addresses none. The ONE
 * implementation of that rule; the two exported readers below differ only in
 * which path SHAPE they accept.
 *
 * `expect` names the segments after the leading empty one, with `null` standing
 * for the slug itself: ['works', null] is `/works/{slug}` and
 * ['works', null, 'recap'] is `/works/{slug}/recap`. The match is STRICT - the
 * path must have exactly that many segments and every literal must be exactly
 * that literal - so `/works`, `/works/`, `/works/a/b` and `/worksy/a` all fall
 * through to the query string, which is what keeps a sub-path from being read
 * as the record itself.
 *
 * The `?id=` fallback is second, and reached from every non-match: the flat
 * shells are what `astro dev` and a metaserve with no artifact loaded serve, and
 * there the query parameter is the only way to name a record. An `?id=` that is
 * present but empty yields '' (not null), so a caller can tell "no id in this
 * URL" from "an id was supplied and it was blank" - the distinction useEntity
 * turns into its not-found state.
 */
function slugFromLocation(
  pathname: string,
  search: string,
  expect: (string | null)[]
): string | null {
  // A leading '/' makes segments[0] the empty string, so an exact match is that
  // empty string followed by exactly the expected segments.
  const segments = pathname.split('/')
  if (segments.length === expect.length + 1 && segments[0] === '') {
    const matched = expect.every((want, i) => want === null || segments[i + 1] === want)
    const slugAt = expect.indexOf(null) + 1
    if (matched) {
      const slug = segments[slugAt] ? decodeSegment(segments[slugAt]) : null
      if (slug) return slug
    }
  }
  return new URLSearchParams(search).get('id')
}

/**
 * The entity slug this location addresses: `/works/{slug}` (the kind's own
 * plural segment) first, the legacy `?id=` second.
 */
export function entitySlugFromLocation(
  pathname: string,
  search: string,
  kind: EntityKind
): string | null {
  return slugFromLocation(pathname, search, [pathSegment[kind], null])
}

/** The community-guide sub-pages a work can have: the story-so-far recap and the
    character guide. metaserve serves each at the work's own route plus this
    literal segment, and only for a work that carries that sidecar. */
export type WorkGuide = 'recap' | 'characters'

/**
 * The WORK slug a guide page addresses: `/works/{slug}/recap` or
 * `/works/{slug}/characters` - the work's own route plus the guide's literal -
 * with the same legacy `?id=` fallback.
 *
 * A guide page is ABOUT a work without being the work's page, which is exactly
 * why the shared matcher above is strict about segment COUNT: `/works/{slug}`
 * never resolves here, and a guide path never resolves as the work itself.
 */
export function workGuideSlugFromLocation(
  pathname: string,
  search: string,
  guide: WorkGuide
): string | null {
  return slugFromLocation(pathname, search, [pathSegment.work, null, guide])
}
