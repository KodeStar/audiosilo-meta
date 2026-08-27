import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  REGION_STORAGE_KEY,
  guessRegions,
  listenChoices,
  preferredRegion,
  readStoredRegion,
  retailerLabel,
  writeStoredRegion,
} from './marketplace'
import type { PurchaseLink } from './api'

/** A minimal in-memory localStorage, optionally one that refuses every call the
    way a browser in private mode (or with storage blocked) does. */
function stubStorage(opts: { throws?: boolean } = {}) {
  const store = new Map<string, string>()
  const fail = () => {
    throw new Error('storage is not available')
  }
  const stub = {
    getItem: (k: string) => (opts.throws ? fail() : (store.get(k) ?? null)),
    setItem: (k: string, v: string) => {
      if (opts.throws) fail()
      store.set(k, v)
    },
    removeItem: (k: string) => void store.delete(k),
    clear: () => store.clear(),
    key: () => null,
    length: 0,
  } as unknown as Storage
  vi.stubGlobal('localStorage', stub)
  return store
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('guessRegions', () => {
  it("reads every tag that states a region, in the reader's preference order", () => {
    expect(guessRegions(['en', 'de-DE', 'en-US'])).toEqual(['de', 'us'])
  })
  it('lowercases the subtag', () => {
    expect(guessRegions(['en-US'])).toEqual(['us'])
    expect(guessRegions(['pt-BR'])).toEqual(['br'])
  })
  it('maps the one alias: BCP-47 GB is the schema region uk', () => {
    expect(guessRegions(['en-GB'])).toEqual(['uk'])
  })
  it('does not let a marketless region suppress the usable one behind it', () => {
    // The Irish/NZ shape: no marketplace is 'ie', but the reader's next tag
    // names one - first-tag-wins would have thrown that away.
    expect(guessRegions(['en-IE', 'en-GB', 'en'])).toEqual(['ie', 'uk'])
  })
  it('deduplicates repeated regions', () => {
    expect(guessRegions(['en-US', 'es-US'])).toEqual(['us'])
  })
  it('sees through a script subtag', () => {
    expect(guessRegions(['zh-Hant-HK'])).toEqual(['hk'])
  })
  it('sees through an extlang subtag', () => {
    expect(guessRegions(['zh-yue-HK'])).toEqual(['hk'])
  })
  it('skips a UN M.49 numeric region rather than stopping on it', () => {
    // No marketplace is a region bloc, so 419 is not a candidate - and the
    // next tag still gets its say.
    expect(guessRegions(['es-419', 'es-ES'])).toEqual(['es'])
  })
  it('returns nothing for languages with no region', () => {
    expect(guessRegions(['en', 'fr', 'de'])).toEqual([])
  })
  it('returns nothing for an empty list', () => {
    expect(guessRegions([])).toEqual([])
  })
  it('skips malformed tags rather than throwing', () => {
    expect(guessRegions(['', '-', 'e-n-US', '!!', 'en_US'])).toEqual([])
    expect(guessRegions(['nonsense!', 'fr-CA'])).toEqual(['ca'])
  })
  it('tolerates surrounding whitespace', () => {
    expect(guessRegions([' en-AU '])).toEqual(['au'])
  })
})

describe('preferredRegion', () => {
  const available = ['de', 'uk', 'us']

  it('takes the first candidate the recording carries', () => {
    expect(preferredRegion(available, ['de', 'us'])).toBe('de')
    expect(preferredRegion(available, ['jp', 'uk'])).toBe('uk')
  })
  it('falls back to us when no candidate applies', () => {
    expect(preferredRegion(available, ['jp'])).toBe('us')
    expect(preferredRegion(available, [])).toBe('us')
  })
  it('falls back to the first listed region when us is absent', () => {
    expect(preferredRegion(['de', 'fr'], ['jp'])).toBe('de')
  })
  it('returns null when there is nothing to choose', () => {
    expect(preferredRegion([], ['us'])).toBeNull()
  })
})

describe('retailerLabel', () => {
  it('names an audible link by its marketplace, exactly as the fact sheet does', () => {
    // The Go twin is internal/serve's purchaseLabel; TestPurchaseLabel pins the
    // same cases there, which is what keeps the two surfaces agreeing.
    expect(retailerLabel('audible', 'us')).toBe('Audible (US)')
    expect(retailerLabel('audible', 'uk')).toBe('Audible (UK)')
    expect(retailerLabel('audible')).toBe('Audible')
  })
  it('names the region-less retailer', () => {
    expect(retailerLabel('libro-fm')).toBe('Libro.fm')
  })
  it('prints an unknown retailer as stated rather than dropping it', () => {
    expect(retailerLabel('some-shop')).toBe('some-shop')
  })
  it('suffixes the marketplace for any retailer, so two regions never read alike', () => {
    expect(retailerLabel('some-shop', 'de')).toBe('some-shop (DE)')
  })
})

describe('listenChoices', () => {
  const link = (retailer: string, id: string, region?: string): PurchaseLink => ({
    retailer,
    id,
    region,
    url: `https://example.test/${retailer}/${region ?? 'any'}/${id}`,
    availability: 'unknown',
  })
  const audibleUS = link('audible', 'B000', 'us')
  const audibleUK = link('audible', 'B001', 'uk')
  const audibleDE = link('audible', 'B002', 'de')
  const libro = link('libro-fm', '9781427209269')

  it('leads with the first candidate marketplace and rows up the rest', () => {
    const { primary, alternates, pills } = listenChoices([audibleUS, audibleUK, audibleDE, libro], ['uk'])
    expect(primary).toBe(audibleUK)
    expect(alternates).toEqual([audibleUS, audibleDE])
    expect(pills).toEqual([libro])
  })
  it('defaults to us when no candidate matches', () => {
    expect(listenChoices([audibleUS, audibleUK], []).primary).toBe(audibleUS)
  })
  it('renders only pills when nothing is region-scoped', () => {
    const { primary, alternates, pills } = listenChoices([libro], ['us'])
    expect(primary).toBeNull()
    expect(alternates).toEqual([])
    expect(pills).toEqual([libro])
  })
  it('keeps a same-region sibling as a fully-labelled pill, not a dead-end row entry', () => {
    // Legal shape: ASIN uniqueness is the (region, asin) pair, so one recording
    // can carry two US products. The second must not render as an
    // 'other marketplace' of the region already shown.
    const secondUS = link('audible', 'B003', 'us')
    const { primary, alternates, pills } = listenChoices([audibleUS, secondUS, audibleUK], ['us'])
    expect(primary).toBe(audibleUS)
    expect(alternates).toEqual([audibleUK])
    expect(pills).toEqual([secondUS])
  })
  it('scopes the alternates row to the primary retailer', () => {
    // The row renders bare region initials, so another retailer's region there
    // would silently navigate somewhere the row does not name - it gets a
    // fully-labelled pill instead, and reaches the chooser when it wins.
    const shopDE = link('some-shop', 'X1', 'de')
    const asUS = listenChoices([audibleUS, audibleUK, shopDE], ['us'])
    expect(asUS.primary).toBe(audibleUS)
    expect(asUS.alternates).toEqual([audibleUK])
    expect(asUS.pills).toEqual([shopDE])

    const asDE = listenChoices([audibleUS, audibleUK, shopDE], ['de'])
    expect(asDE.primary).toBe(shopDE)
    expect(asDE.alternates).toEqual([])
    expect(asDE.pills).toEqual([audibleUS, audibleUK])
  })
  it('lands every link in exactly one bucket', () => {
    const links = [audibleUS, audibleUK, audibleDE, libro]
    const { primary, alternates, pills } = listenChoices(links, ['uk'])
    expect([primary, ...alternates, ...pills].filter(Boolean)).toHaveLength(links.length)
  })
})

describe('storage helpers', () => {
  it('round-trips a pick under the namespaced key', () => {
    const store = stubStorage()
    writeStoredRegion('uk')
    expect(store.get(REGION_STORAGE_KEY)).toBe('uk')
    expect(readStoredRegion()).toBe('uk')
  })
  it('reads null when nothing is stored', () => {
    stubStorage()
    expect(readStoredRegion()).toBeNull()
  })
  it('degrades to no preference when storage throws', () => {
    stubStorage({ throws: true })
    expect(readStoredRegion()).toBeNull()
    expect(() => writeStoredRegion('uk')).not.toThrow()
  })
  it('degrades when there is no localStorage at all', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(readStoredRegion()).toBeNull()
    expect(() => writeStoredRegion('uk')).not.toThrow()
  })
})
