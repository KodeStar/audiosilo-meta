// Map a source's raw genre claims onto this project's controlled genre
// vocabulary - the browser-side twin of the Go importer's audiblegenres.go.
//
// LICENSING.md ("Genres") forbids storing a retailer's genre taxonomy verbatim:
// its node names, hierarchy and typed tags are that retailer's editorial
// arrangement. So a claim never travels as itself: it is either resolved to a
// slug from schema/common.schema.json #/$defs/genre or DROPPED.
//
// SINGLE SOURCE OF TRUTH: the mapping table is imported directly from
// internal/importer/audiblegenres.json - the same file the Go importer embeds.
// The import reaches outside site/ deliberately (Vite/Rollup resolve and inline
// it at build time, and `astro check` types it), so there is no second copy to
// drift. Regenerating the table updates both consumers at once. The image build
// copies the file to the matching relative position, because its site stage's
// context is site/ alone - see the COPY in the repo's Dockerfile.
//
// The lookup order MIRRORS Go's genreTable.lookup so a libex-seeded prefill and a
// bulk `metaimport libex` of the same record resolve identically: browse-node id
// first (stable across locales), then the lower-cased, trimmed display name.

import table from '../../../internal/importer/audiblegenres.json'
import type { LibexGenreClaim } from './libex'

// The table's on-disk key is "by_asin" - its keys are Audible browse-node ids,
// not product ASINs (see the Go file's comment on the same field name).
//
// Both maps are NULL-PROTOTYPE copies, because the lookup key is attacker-shaped
// data: a claim named "constructor" (or "toString", "valueOf", ...) would
// otherwise resolve through Object.prototype to a FUNCTION, which is not a
// vocabulary slug, lies about the declared `string | undefined` type, and would
// ride into the confirmation card, the prefilled issue URL and the submission.
// Object.create(null) has no prototype chain to inherit from, so an unmapped
// claim is undefined whatever it is called.
const BY_NODE: Record<string, string | undefined> = Object.assign(
  Object.create(null),
  table.by_asin
)
const BY_NAME: Record<string, string | undefined> = Object.assign(
  Object.create(null),
  table.by_name
)

/**
 * Resolve one claim to a vocabulary slug, or undefined when it does not map.
 * Node id first, then the normalized name (mirrors Go genreTable.lookup).
 */
export function mapGenreClaim(claim: LibexGenreClaim): string | undefined {
  const node = claim.node?.trim()
  if (node) {
    const byNode = BY_NODE[node]
    if (byNode) return byNode
  }
  const name = claim.name?.trim().toLowerCase()
  if (!name) return undefined
  return BY_NAME[name] ?? undefined
}

/**
 * Resolve a record's claims to a deduplicated, ascending-sorted list of
 * vocabulary slugs. Unmapped claims are dropped silently - the reader is not
 * shown a retailer category we cannot store, and the sort matches the order the
 * data tree requires (checkGenresSorted).
 */
export function mapGenreClaims(claims: LibexGenreClaim[]): string[] {
  const out = new Set<string>()
  for (const claim of claims) {
    const slug = mapGenreClaim(claim)
    if (slug) out.add(slug)
  }
  return [...out].sort()
}
