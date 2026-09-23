// The catalogue's slug shape, mirrored from pkg/model's Go rule
// (`^[a-z0-9]+(-[a-z0-9]+)*$`, MAX_SLUG_LEN 100). A LEAF module on purpose -
// lib/watchlist.ts imports only this (see its own header) and must not drag in
// builder.ts, which the site header's bundled script loads eagerly.

/** The schema's maxLength on slugs (characters.schema.json and the catalogue's
    own work/series/person ids alike). */
export const MAX_SLUG_LEN = 100

export const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/

/** Format AND length: the schema caps slugs at MAX_SLUG_LEN characters. */
export function isValidSlug(s: string): boolean {
  return s.length <= MAX_SLUG_LEN && SLUG_RE.test(s)
}
