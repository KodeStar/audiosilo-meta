// The catalogue's slug shape, mirrored from pkg/model's Go rule of record -
// `slugPattern`/`MaxSlugLen` in pkg/model/location.go
// (`^[a-z0-9]+(-[a-z0-9]+)*$`, MAX_SLUG_LEN 100). A LEAF module on purpose -
// lib/watchlist.ts does not import lib/builder.ts (it is kept a leaf for the
// header chunk), which the site header's bundled script loads eagerly.

/** The schema's maxLength on slugs (characters.schema.json and the catalogue's
    own work/series/person ids alike). */
export const MAX_SLUG_LEN = 100

const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/

/** Format AND length: the schema caps slugs at MAX_SLUG_LEN characters. */
export function isValidSlug(s: string): boolean {
  return s.length <= MAX_SLUG_LEN && SLUG_RE.test(s)
}
