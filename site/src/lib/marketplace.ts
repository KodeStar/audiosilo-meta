// Which retailer marketplace a reader is shown first on a work page, and how an
// explicit pick is remembered. Kept framework-free so it can be unit-tested; the
// React island in WorkDetail.tsx holds the pick as ordinary lifted state and
// persists it through the storage helpers below.
//
// The region VOCABULARY is deliberately NOT mirrored here. The enum lives in
// schema/common.schema.json and the marketplace hosts in
// internal/serve/purchase_links.go, so a copy on the site would be a third
// spelling that drifts the day a marketplace is added. Instead a guessed or
// stored region is only ever used when it is one of the regions a recording's
// own links carry - validity is decided by the data on the page.
//
// The server renders no default at all (its pages are publicly cached and
// golden-file tested), so choosing one is entirely this layer's job. That is
// affordable because a wrong guess costs one click: every other recorded region
// is a real link beside the default.

import type { PurchaseLink } from './api'

/** The localStorage key an explicit pick is remembered under. Namespaced,
    because the site shares an origin with the API. */
export const REGION_STORAGE_KEY = 'audiosilo-meta:marketplace'

// A language tag's region subtag: the two-letter subtag after the language, an
// optional extlang ("zh-yue-HK") and an optional script ("zh-Hant-HK").
// Matched rather than parsed through Intl.Locale, which throws on the
// malformed tags a browser really does report. Three-digit UN M.49 regions
// ("es-419") are deliberately NOT matched: no marketplace is a region bloc, so
// the tag is skipped and a later tag gets its say.
const REGION_SUBTAG = /^[A-Za-z]{2,3}(?:-[A-Za-z]{3})?(?:-[A-Za-z]{4})?-([A-Za-z]{2})(?:-|$)/

/** The reader's likely marketplaces, one per language tag that states a region,
    in the reader's own preference order and deduplicated. Every tag is read -
    not just the first that states a region - because an early region with no
    marketplace ("en-IE, en-GB, en") must not suppress the usable one behind
    it. navigator.languages is the ONE signal used: no timezone table and no
    GeoIP, because a cheap guess is the right trade when correcting it is a
    single click.

    `gb -> uk` is the single alias - BCP-47 spells the United Kingdom GB and the
    schema's region enum spells it uk (libex-map's mapLibexRegion applies the
    same alias at the libex boundary). Every other subtag is lowercased and used
    as stated; whether it names a marketplace is decided by preferredRegion
    against the links actually present. */
export function guessRegions(languages: readonly string[]): string[] {
  const regions: string[] = []
  for (const tag of languages) {
    const m = REGION_SUBTAG.exec(tag.trim())
    if (!m) continue
    const region = m[1].toLowerCase()
    const mapped = region === 'gb' ? 'uk' : region
    if (!regions.includes(mapped)) regions.push(mapped)
  }
  return regions
}

/** The region to show first: the first candidate the recording can honour
    (the caller lists them in priority order - the stored pick ahead of the
    browser's guesses), else `us`, else whatever the recording lists first.
    Deterministic, and null only when there is nothing to choose between. */
export function preferredRegion(
  available: readonly string[],
  candidates: readonly string[]
): string | null {
  for (const c of candidates) {
    if (available.includes(c)) return c
  }
  if (available.includes('us')) return 'us'
  return available[0] ?? null
}

/** How a link reads to a person: the retailer, plus the marketplace where the
    identifier is region-scoped - for ANY retailer, so two regions of one
    retailer never render as identical labels. The EXACT twin of
    internal/serve's purchaseLabel, so the server-rendered fact sheet and the
    hydrated island spell one link the same way. An unknown retailer is printed
    as the API stated it: the URL is still a real route to the recording, and
    rendering nothing would hide a fact the response carries. */
export function retailerLabel(retailer: string, region?: string): string {
  let name = retailer
  switch (retailer) {
    case 'audible':
      name = 'Audible'
      break
    case 'libro-fm':
      name = 'Libro.fm'
      break
  }
  return region ? `${name} (${region.toUpperCase()})` : name
}

/** A purchase link that names its marketplace. Region presence is the data's
    own distinction (internal/serve/purchase_links.go emits `region` exactly on
    marketplace-scoped identifiers), so the split does not key on the retailer
    name and a region-scoped retailer the server learns to derive tomorrow
    reaches the marketplace chooser by construction. */
export type RegionalLink = PurchaseLink & { region: string }

function isRegional(l: PurchaseLink): l is RegionalLink {
  return Boolean(l.region)
}

/** What ListenLinks renders, composed here so the rules are unit-testable:

    - `primary`: the link for the chosen marketplace (preferredRegion over the
      regional links' regions and the caller's candidates).
    - `alternates`: the primary RETAILER's other marketplaces - the "one click
      to correct a wrong guess" row. Scoped to the primary's retailer because
      the row renders bare region initials; another retailer's region there
      would silently navigate somewhere the row does not name.
    - `pills`: every other link, each carrying its own full label - the
      region-less retailers (Libro.fm), a second retailer's regional links, and
      a same-region sibling of the primary (legal: ASIN uniqueness is the
      (region, asin) pair, so one recording can carry two US products).

    Every link the API stated lands in exactly one of the three. */
export function listenChoices(
  links: readonly PurchaseLink[],
  candidates: readonly string[]
): { primary: RegionalLink | null; alternates: RegionalLink[]; pills: PurchaseLink[] } {
  const regional = links.filter(isRegional)
  const region = preferredRegion(
    regional.map((l) => l.region),
    candidates
  )
  const primary = regional.find((l) => l.region === region) ?? null

  const alternates: RegionalLink[] = []
  const pills: PurchaseLink[] = []
  for (const l of links) {
    if (l === primary) continue
    if (primary && isRegional(l) && l.retailer === primary.retailer && l.region !== primary.region) {
      alternates.push(l)
    } else {
      pills.push(l)
    }
  }
  return { primary, alternates, pills }
}

/** The stored pick, or null. Every failure - private mode, blocked storage, no
    localStorage at all - degrades to "no stored preference" rather than
    throwing, so a reader with storage off still gets a working page. */
export function readStoredRegion(): string | null {
  try {
    return globalThis.localStorage?.getItem(REGION_STORAGE_KEY) || null
  } catch {
    return null
  }
}

/** Remember an explicit pick. Silent on failure for the same reason: the choice
    still applies to the page in front of the reader, it just will not outlive it. */
export function writeStoredRegion(region: string): void {
  try {
    globalThis.localStorage?.setItem(REGION_STORAGE_KEY, region)
  } catch {
    /* storage blocked - the pick still applies for this page load */
  }
}
