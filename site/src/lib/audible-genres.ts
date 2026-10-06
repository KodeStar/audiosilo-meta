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
// (Go additionally consults the table's `by_path` between the two, for sources
// that state a category ladder of names rather than a node id - OpenAudible's
// `genre` field. A libex claim always carries its node, so that step never
// decides anything here and is deliberately not mirrored.)
//
// The FORMAT rule is mirrored too (Go's formatDerived / mapGenres): a claim that is
// a format node (Audio Performances & Dramatizations, Radio, Film & TV, ... in
// every marketplace), or an ancestor of one the record states that it reaches
// through no subject descendant, yields its genre only when nothing else the
// record states maps. So a radio dramatization of a crime novel prefills as a
// crime novel, exactly what `metaimport libex` stores for the same record. The
// table's `format_tree` is already in the shape the rule needs (scripts/genrepaths
// derives each node's format flag and closed ancestor list), so this only looks
// nodes up.

// NAMED imports, not the default: Vite exposes a JSON file's top-level keys as
// tree-shakeable named exports, so the bundle carries the tables this module
// reads and never the by_path or format_paths tables (Go-only - see above), which a default
// import would ship in every client chunk that maps a genre.
import {
  by_asin,
  by_name,
  format_tree,
} from '../../../internal/importer/audiblegenres.json'
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
  by_asin
)
const BY_NAME: Record<string, string | undefined> = Object.assign(
  Object.create(null),
  by_name
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

// The derived format tree: node id -> its format flag and ancestors.
// Null-prototype for the same reason the two maps above are.
const FORMAT_TREE: Record<string, { format?: boolean; ancestors: string[] } | undefined> =
  Object.assign(Object.create(null), format_tree)

/**
 * Which claims are FORMAT-DERIVED (Go's formatIndex.derived): a format node, or
 * an ancestor of a format node the record states, that the record reaches
 * through no non-format descendant. Null when none is.
 */
export function formatDerived(claims: LibexGenreClaim[]): boolean[] | null {
  const nodes = claims.map((c) => {
    const node = c.node?.trim() ?? ''
    return node && FORMAT_TREE[node] ? node : ''
  })
  if (!nodes.some((n) => FORMAT_TREE[n]?.format)) return null
  const formatish = new Set<string>()
  for (const n of nodes) {
    const fn = FORMAT_TREE[n]
    if (fn?.format) {
      formatish.add(n)
      for (const a of fn.ancestors) formatish.add(a)
    }
  }
  const justified = new Set<string>()
  for (const n of nodes) {
    if (!n || formatish.has(n)) continue
    for (const a of FORMAT_TREE[n]?.ancestors ?? []) justified.add(a)
  }
  return nodes.map((n) => n !== '' && formatish.has(n) && !justified.has(n))
}

/**
 * Resolve a record's claims to a deduplicated, ascending-sorted list of
 * vocabulary slugs. Unmapped claims are dropped silently - the reader is not
 * shown a retailer category we cannot store, and the sort matches the order the
 * data tree requires (checkGenresSorted). A format-derived claim's genre is kept
 * only when nothing else maps (the format rule above).
 */
export function mapGenreClaims(claims: LibexGenreClaim[]): string[] {
  const slugs = claims.map(mapGenreClaim)
  const derived = formatDerived(claims)
  const collect = (skip: boolean[] | null): string[] => {
    const out = new Set<string>()
    slugs.forEach((slug, i) => {
      if (slug && !skip?.[i]) out.add(slug)
    })
    return [...out].sort()
  }
  const out = collect(derived)
  return out.length === 0 && derived ? collect(null) : out
}
