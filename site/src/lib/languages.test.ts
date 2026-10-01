import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  LANGUAGES_STORAGE_KEY,
  MAX_LANGUAGES,
  activeLanguages,
  badgeContext,
  browserLanguages,
  censusOptions,
  chosenPrefs,
  fromParam,
  joinNames,
  languageName,
  languagesFromSearch,
  nativeLanguageName,
  needsBadge,
  normalizeLanguages,
  parsePrefs,
  primarySubtag,
  readLanguagePrefs,
  selectorSummary,
  suggestion,
  toParam,
  withEnglish,
  writeLanguagePrefs,
  type LanguagePrefs,
} from './languages'
import { stubStorage } from './test-support'

afterEach(() => {
  vi.unstubAllGlobals()
})

const census = censusOptions([
  { language: 'en', works: 244625 },
  { language: 'de', works: 21512 },
  { language: 'fr', works: 4338 },
])

describe('primarySubtag', () => {
  it('reduces a tag to its primary subtag, lowercased', () => {
    expect(primarySubtag('de')).toBe('de')
    expect(primarySubtag('de-AT')).toBe('de')
    expect(primarySubtag('pt_BR')).toBe('pt')
    expect(primarySubtag(' EN-gb ')).toBe('en')
    expect(primarySubtag('zh-Hant-HK')).toBe('zh')
  })

  it('answers nothing for what is not a language tag', () => {
    expect(primarySubtag('')).toBe('')
    expect(primarySubtag(null)).toBe('')
    expect(primarySubtag('*')).toBe('')
    expect(primarySubtag('german')).toBe('')
    expect(primarySubtag('d')).toBe('')
  })
})

describe('the lang parameter', () => {
  it('is canonical: primary subtags, deduplicated and sorted', () => {
    expect(toParam(['fr', 'de-AT', 'de'])).toBe('de,fr')
    expect(toParam([])).toBe('')
  })

  it('reads back tolerantly, dropping what the server would refuse', () => {
    expect(fromParam('de,EN')).toEqual(['de', 'en'])
    expect(fromParam('de-at, fr ,de')).toEqual(['de', 'fr'])
    expect(fromParam('de,<script>,')).toEqual(['de'])
    expect(fromParam('')).toEqual([])
    expect(fromParam(null)).toEqual([])
  })

  it('never names more languages than the server accepts', () => {
    const many = ['aa', 'bb', 'cc', 'dd', 'ee', 'ff', 'gg', 'hh', 'ii', 'jj']
    expect(normalizeLanguages(many)).toHaveLength(MAX_LANGUAGES)
    expect(fromParam(many.join(','))).toHaveLength(MAX_LANGUAGES)
  })
})

describe('the active filter', () => {
  const stored = chosenPrefs(['fr'])

  it('is a shared link’s filter first', () => {
    expect(languagesFromSearch('?q=harry&lang=de')).toEqual(['de'])
    expect(activeLanguages('?q=harry&lang=de', stored)).toEqual(['de'])
  })

  it('is the stored preference when the URL names none', () => {
    expect(activeLanguages('?q=harry', stored)).toEqual(['fr'])
    // An empty or garbage lang names no filter, so it cannot override the
    // reader's own choice.
    expect(activeLanguages('?lang=', stored)).toEqual(['fr'])
    expect(activeLanguages('?lang=%3F%3F', stored)).toEqual(['fr'])
  })

  it('is all languages by default', () => {
    expect(activeLanguages('', null)).toEqual([])
  })
})

describe('badges', () => {
  it('judge against the active filter when there is one, else the browser', () => {
    expect(badgeContext(['de'], ['en'])).toEqual(['de'])
    expect(badgeContext([], ['en', 'fr'])).toEqual(['en', 'fr'])
  })

  it('mark an item outside the context, by primary subtag', () => {
    expect(needsBadge('de', ['en'])).toBe(true)
    expect(needsBadge('en-GB', ['en'])).toBe(false)
    expect(needsBadge('de-at', ['de', 'en'])).toBe(false)
  })

  it('never mark an unknown language or against an empty context', () => {
    expect(needsBadge(undefined, ['en'])).toBe(false)
    expect(needsBadge('', ['en'])).toBe(false)
    expect(needsBadge('de', [])).toBe(false)
  })
})

describe('censusOptions', () => {
  it('groups regional tags under their primary subtag, most works first', () => {
    expect(
      censusOptions([
        { language: 'en', works: 10 },
        { language: 'de', works: 3 },
        { language: 'de-at', works: 2 },
        { language: 'fr', works: 5 },
        { language: 'es', works: 5 },
      ])
    ).toEqual([
      { language: 'en', works: 10 },
      { language: 'de', works: 5 },
      { language: 'es', works: 5 },
      { language: 'fr', works: 5 },
    ])
  })

  it('is empty before the server reports a census', () => {
    expect(censusOptions(undefined)).toEqual([])
  })
})

describe('suggestion', () => {
  it('offers the browser’s non-English languages the catalogue holds', () => {
    expect(suggestion(null, ['de', 'en'], census)).toEqual(['de'])
    expect(suggestion(null, ['fr', 'de', 'en'], census)).toEqual(['fr', 'de'])
  })

  it('never offers English, nor a language the catalogue does not hold', () => {
    expect(suggestion(null, ['en'], census)).toEqual([])
    expect(suggestion(null, ['ja', 'en'], census)).toEqual([])
  })

  it('is silent once anything is stored - a choice or a dismissal', () => {
    expect(suggestion(chosenPrefs([]), ['de'], census)).toEqual([])
    expect(suggestion(chosenPrefs(['fr']), ['de'], census)).toEqual([])
    const dismissed: LanguagePrefs = { version: 1, languages: [], promptDismissed: true }
    expect(suggestion(dismissed, ['de'], census)).toEqual([])
  })

  it('adds English for the second choice', () => {
    expect(withEnglish(['de'])).toEqual(['de', 'en'])
  })
})

describe('names', () => {
  it('labels in English and asks in the language itself', () => {
    expect(languageName('de')).toBe('German')
    expect(nativeLanguageName('de')).toBe('Deutsch')
    expect(nativeLanguageName('fr')).toBe('Français')
  })

  it('summarizes the selector', () => {
    expect(selectorSummary([])).toBe('All')
    expect(selectorSummary(['de'])).toBe('German')
    expect(selectorSummary(['de', 'en'])).toBe('2 languages')
  })

  it('joins a list the way a sentence does', () => {
    expect(joinNames(['A'])).toBe('A')
    expect(joinNames(['A', 'B'])).toBe('A and B')
    expect(joinNames(['A', 'B', 'C'])).toBe('A, B and C')
  })
})

describe('storage', () => {
  it('round-trips an explicit choice, normalized', () => {
    const map = stubStorage()
    writeLanguagePrefs(chosenPrefs(['de-AT', 'en']))
    expect(JSON.parse(map.get(LANGUAGES_STORAGE_KEY) ?? '')).toEqual({
      version: 1,
      languages: ['de', 'en'],
      promptDismissed: true,
    })
    expect(readLanguagePrefs()).toEqual(chosenPrefs(['de', 'en']))
  })

  it('reads nothing stored as null, the one state the prompt may appear in', () => {
    stubStorage()
    expect(readLanguagePrefs()).toBeNull()
  })

  it('tolerates a malformed or foreign document', () => {
    expect(parsePrefs('not json')).toBeNull()
    expect(parsePrefs('{"version":2,"languages":["de"]}')).toBeNull()
    expect(parsePrefs('{"version":1,"languages":["de", 7, "??"]}')).toEqual({
      version: 1,
      languages: ['de'],
      promptDismissed: false,
    })
  })

  it('degrades when storage is blocked', () => {
    stubStorage({ throws: true })
    expect(readLanguagePrefs()).toBeNull()
    expect(() => writeLanguagePrefs(chosenPrefs(['de']))).not.toThrow()
  })

  it('degrades when there is no localStorage at all', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(readLanguagePrefs()).toBeNull()
    expect(() => writeLanguagePrefs(chosenPrefs(['de']))).not.toThrow()
  })
})

describe('browserLanguages', () => {
  it('reduces navigator.languages to primary subtags in preference order', () => {
    expect(browserLanguages(['de-DE', 'de', 'en-US', 'en'])).toEqual(['de', 'en'])
    expect(browserLanguages(undefined)).toEqual([])
  })
})
