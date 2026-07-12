import { describe, it, expect } from 'vitest'
import {
  slugify,
  isValidSlug,
  parseChapter,
  parseAliases,
  validateCharacters,
  validateRecaps,
  buildCharactersObject,
  buildRecapsObject,
  serializeCanonical,
  seedCharactersFromSibling,
  parsePositionValue,
  nearestLowerSibling,
  moveItem,
  emptyCharacterDraft,
  emptyRecapsDraft,
  CAPS,
  type CharacterDraft,
  type RecapsDraft,
} from './builder'
import type { Character } from './api'

function charDraft(extra: Partial<CharacterDraft> = {}): CharacterDraft {
  return { ...emptyCharacterDraft(), id: 'el', name: 'El', reveal: '1', ...extra }
}

function recapsDraft(extra: Partial<RecapsDraft> = {}): RecapsDraft {
  return {
    entries: [{ through: '3', scope: 'book', text: 'So far.' }],
    inShort: '',
    ending: '',
    ...extra,
  }
}

describe('slugify', () => {
  it('lowercases and hyphenates a display name', () => {
    expect(slugify('Orion Lake')).toBe('orion-lake')
    expect(slugify('Gwen Higgins')).toBe('gwen-higgins')
  })

  it('strips diacritics and collapses punctuation runs', () => {
    expect(slugify('Aadhya')).toBe('aadhya')
    expect(slugify("D'Artagnan")).toBe('d-artagnan')
    expect(slugify('Zoë  --  Washburne!!')).toBe('zoe-washburne')
    expect(slugify('El (Galadriel)')).toBe('el-galadriel')
  })

  it('trims leading/trailing hyphens and yields a valid slug or empty', () => {
    expect(slugify('  The King  ')).toBe('the-king')
    expect(slugify('---')).toBe('')
    expect(slugify('!!!')).toBe('')
    expect(isValidSlug(slugify('Orion Lake'))).toBe(true)
  })
})

describe('isValidSlug', () => {
  it('accepts lowercase hyphen-joined tokens only', () => {
    expect(isValidSlug('el')).toBe(true)
    expect(isValidSlug('orion-lake')).toBe(true)
    expect(isValidSlug('a1-b2')).toBe(true)
  })
  it('rejects uppercase, spaces, doubled/edge hyphens and empties', () => {
    expect(isValidSlug('El')).toBe(false)
    expect(isValidSlug('orion lake')).toBe(false)
    expect(isValidSlug('-el')).toBe(false)
    expect(isValidSlug('el-')).toBe(false)
    expect(isValidSlug('orion--lake')).toBe(false)
    expect(isValidSlug('')).toBe(false)
  })
})

describe('parseChapter', () => {
  it('parses non-negative whole numbers', () => {
    expect(parseChapter('0')).toBe(0)
    expect(parseChapter('13')).toBe(13)
    expect(parseChapter('  7 ')).toBe(7)
  })
  it('rejects decimals, signs, blanks and non-numeric', () => {
    expect(parseChapter('')).toBeNull()
    expect(parseChapter('2.5')).toBeNull()
    expect(parseChapter('-1')).toBeNull()
    expect(parseChapter('one')).toBeNull()
    expect(parseChapter('3a')).toBeNull()
  })
})

describe('parseAliases', () => {
  it('splits, trims and drops empties', () => {
    expect(parseAliases('Galadriel Higgins, Galadriel')).toEqual(['Galadriel Higgins', 'Galadriel'])
    expect(parseAliases('  , A ,, B , ')).toEqual(['A', 'B'])
    expect(parseAliases('')).toEqual([])
  })
})

describe('validateCharacters', () => {
  it('accepts a minimal valid card', () => {
    const v = validateCharacters([charDraft()])
    expect(v.ok).toBe(true)
    expect(v.form).toEqual([])
    expect(v.cards[0]).toEqual({})
  })

  it('requires at least one card', () => {
    const v = validateCharacters([])
    expect(v.ok).toBe(false)
    expect(v.form.length).toBe(1)
  })

  it('flags empty/invalid id, empty name and a bad reveal', () => {
    const v = validateCharacters([charDraft({ id: 'Bad Id', name: '  ', reveal: '2.5' })])
    expect(v.ok).toBe(false)
    expect(v.cards[0].id).toBeDefined()
    expect(v.cards[0].name).toBeDefined()
    expect(v.cards[0].reveal).toBeDefined()
  })

  it('requires an id', () => {
    const v = validateCharacters([charDraft({ id: '' })])
    expect(v.cards[0].id).toBe('An id is required.')
  })

  it('marks duplicate ids on every offending card', () => {
    const v = validateCharacters([
      charDraft({ id: 'el', name: 'El' }),
      charDraft({ id: 'el', name: 'El Again' }),
      charDraft({ id: 'orion', name: 'Orion' }),
    ])
    expect(v.ok).toBe(false)
    expect(v.cards[0].id).toMatch(/Duplicate/)
    expect(v.cards[1].id).toMatch(/Duplicate/)
    expect(v.cards[2].id).toBeUndefined()
  })

  it('enforces the description cap and validates the wikidata QID', () => {
    const over = validateCharacters([charDraft({ description: 'x'.repeat(CAPS.description + 1) })])
    expect(over.cards[0].description).toBeDefined()

    const okLen = validateCharacters([charDraft({ description: 'x'.repeat(CAPS.description) })])
    expect(okLen.cards[0].description).toBeUndefined()

    expect(validateCharacters([charDraft({ wikidata: '12345' })]).cards[0].wikidata).toBeDefined()
    expect(validateCharacters([charDraft({ wikidata: 'Q42' })]).cards[0].wikidata).toBeUndefined()
    expect(validateCharacters([charDraft({ wikidata: '' })]).cards[0].wikidata).toBeUndefined()
  })

  it('accepts reveal 0 (prior-book knowledge)', () => {
    expect(validateCharacters([charDraft({ reveal: '0' })]).ok).toBe(true)
  })
})

describe('validateRecaps', () => {
  it('accepts a minimal valid entry', () => {
    const v = validateRecaps(recapsDraft())
    expect(v.ok).toBe(true)
    expect(v.form).toEqual([])
  })

  it('requires at least one entry', () => {
    const v = validateRecaps(recapsDraft({ entries: [] }))
    expect(v.ok).toBe(false)
    expect(v.form.length).toBe(1)
  })

  it('flags a bad through chapter and empty text', () => {
    const v = validateRecaps(recapsDraft({ entries: [{ through: '-1', scope: '', text: '  ' }] }))
    expect(v.entries[0].through).toBeDefined()
    expect(v.entries[0].text).toBeDefined()
  })

  it('marks duplicate through chapters', () => {
    const v = validateRecaps(
      recapsDraft({
        entries: [
          { through: '3', scope: 'book', text: 'a' },
          { through: '3', scope: 'book', text: 'b' },
        ],
      })
    )
    expect(v.ok).toBe(false)
    expect(v.entries[0].through).toMatch(/Duplicate/)
    expect(v.entries[1].through).toMatch(/Duplicate/)
  })

  it('enforces the recap text, in_short and ending caps', () => {
    const longText = validateRecaps(
      recapsDraft({ entries: [{ through: '1', scope: '', text: 'x'.repeat(CAPS.recapText + 1) }] })
    )
    expect(longText.entries[0].text).toBeDefined()

    expect(validateRecaps(recapsDraft({ inShort: 'x'.repeat(CAPS.inShort + 1) })).inShort).toBeDefined()
    expect(validateRecaps(recapsDraft({ ending: 'x'.repeat(CAPS.ending + 1) })).ending).toBeDefined()
    // At the cap exactly is fine.
    expect(validateRecaps(recapsDraft({ inShort: 'x'.repeat(CAPS.inShort) })).ok).toBe(true)
  })

  it('treats empty summaries as absent (optional)', () => {
    expect(validateRecaps(recapsDraft({ inShort: '', ending: '' })).ok).toBe(true)
  })
})

describe('buildCharactersObject', () => {
  it('emits the fixed contract fields and required per-card fields', () => {
    const file = buildCharactersObject('a-deadly-education', [charDraft({ id: 'el', name: 'El', reveal: '1' })])
    expect(file.work).toBe('a-deadly-education')
    expect(file.license).toBe('CC-BY-SA-3.0')
    expect(file.sources).toEqual([{ type: 'community' }])
    expect(file.characters[0]).toEqual({ id: 'el', name: 'El', reveal: { chapter: 1 } })
  })

  it('includes optional fields only when present and trims strings', () => {
    const file = buildCharactersObject('w', [
      charDraft({
        id: 'el',
        name: '  El  ',
        aliasesText: 'Galadriel, Gal',
        role: 'protagonist',
        reveal: '2',
        description: '  A senior.  ',
        wikidata: 'Q7',
      }),
    ])
    expect(file.characters[0]).toEqual({
      id: 'el',
      name: 'El',
      aliases: ['Galadriel', 'Gal'],
      role: 'protagonist',
      reveal: { chapter: 2 },
      description: 'A senior.',
      xref: { wikidata: 'Q7' },
    })
  })

  it('omits aliases/role/description/xref when empty', () => {
    const file = buildCharactersObject('w', [charDraft()])
    const c = file.characters[0]
    expect('aliases' in c).toBe(false)
    expect('role' in c).toBe(false)
    expect('description' in c).toBe(false)
    expect('xref' in c).toBe(false)
  })
})

describe('buildRecapsObject', () => {
  it('builds entries and includes the optional summaries only when present', () => {
    const withSummaries = buildRecapsObject('w', {
      entries: [{ through: '3', scope: 'series', text: '  Previously.  ' }],
      inShort: '  The whole arc.  ',
      ending: '  It ends thus.  ',
    })
    expect(withSummaries.work).toBe('w')
    expect(withSummaries.license).toBe('CC-BY-SA-3.0')
    expect(withSummaries.recaps[0]).toEqual({ through: { chapter: 3 }, scope: 'series', text: 'Previously.' })
    expect(withSummaries.in_short).toBe('The whole arc.')
    expect(withSummaries.ending).toBe('It ends thus.')
  })

  it('omits scope, in_short and ending when empty', () => {
    const file = buildRecapsObject('w', {
      entries: [{ through: '1', scope: '', text: 'a' }],
      inShort: '',
      ending: '',
    })
    expect('scope' in file.recaps[0]).toBe(false)
    expect('in_short' in file).toBe(false)
    expect('ending' in file).toBe(false)
  })
})

describe('serializeCanonical', () => {
  it('sorts keys recursively, 2-space indents, and ends with a newline', () => {
    const out = serializeCanonical({ b: 1, a: { d: 2, c: 3 } })
    expect(out).toBe('{\n  "a": {\n    "c": 3,\n    "d": 2\n  },\n  "b": 1\n}\n')
  })

  it('preserves array order while sorting object keys within items', () => {
    const out = serializeCanonical({ list: [{ y: 1, x: 2 }] })
    expect(out).toBe('{\n  "list": [\n    {\n      "x": 2,\n      "y": 1\n    }\n  ]\n}\n')
  })

  it('produces the exemplar key order for a characters file', () => {
    const file = buildCharactersObject('a-deadly-education', [
      charDraft({ id: 'el', name: 'El', aliasesText: 'Galadriel', role: 'protagonist', reveal: '1', description: 'A senior.' }),
    ])
    const lines = serializeCanonical(file).split('\n')
    // Top-level keys are alphabetical: characters, license, sources, work.
    expect(lines[1]).toBe('  "characters": [')
    // Per-card keys are alphabetical: aliases, description, id, name, reveal,
    // role. Scope to the characters array (the sources item also has 6-space
    // keys) by stopping at the array's closing bracket.
    const end = lines.indexOf('  ],')
    const cardKeys = lines
      .slice(0, end)
      .filter((l) => /^ {6}"/.test(l))
      .map((l) => l.trim().split('"')[1])
    expect(cardKeys).toEqual(['aliases', 'description', 'id', 'name', 'reveal', 'role'])
  })
})

describe('seedCharactersFromSibling', () => {
  const cast: Character[] = [
    {
      id: 'el',
      name: 'El',
      aliases: ['Galadriel'],
      role: 'protagonist',
      reveal: { chapter: 1 },
      description: 'A senior at the Scholomance.',
      xref: { wikidata: 'Q7' },
    },
    { id: 'orion', name: 'Orion Lake', reveal: { chapter: 1 } },
  ]

  it('copies identity, clears descriptions, resets reveal to 0 and flags seeded', () => {
    const seeded = seedCharactersFromSibling(cast)
    expect(seeded[0]).toEqual({
      id: 'el',
      name: 'El',
      aliasesText: 'Galadriel',
      role: 'protagonist',
      reveal: '0',
      description: '',
      wikidata: 'Q7',
      seeded: true,
    })
    expect(seeded[1]).toEqual({
      id: 'orion',
      name: 'Orion Lake',
      aliasesText: '',
      role: '',
      reveal: '0',
      description: '',
      wikidata: '',
      seeded: true,
    })
  })
})

describe('parsePositionValue', () => {
  it('parses integers, decimals and range starts', () => {
    expect(parsePositionValue('1')).toBe(1)
    expect(parsePositionValue('2.5')).toBe(2.5)
    expect(parsePositionValue('1-3.5')).toBe(1)
  })
  it('returns null for non-numeric positions', () => {
    expect(parsePositionValue('')).toBeNull()
    expect(parsePositionValue('prequel')).toBeNull()
  })
})

describe('nearestLowerSibling', () => {
  const works = [
    { position: '1', work: { id: 'book-1' } },
    { position: '2', work: { id: 'book-2' } },
    { position: '2.5', work: { id: 'book-2-5' } },
    { position: '3', work: { id: 'book-3' } },
  ]

  it('returns the highest sibling strictly below the current position', () => {
    expect(nearestLowerSibling(works, 'book-3')?.work.id).toBe('book-2-5')
    expect(nearestLowerSibling(works, 'book-2-5')?.work.id).toBe('book-2')
    expect(nearestLowerSibling(works, 'book-2')?.work.id).toBe('book-1')
  })

  it('returns null for the first book, an unknown id, or an unparseable position', () => {
    expect(nearestLowerSibling(works, 'book-1')).toBeNull()
    expect(nearestLowerSibling(works, 'nope')).toBeNull()
    expect(nearestLowerSibling([{ position: 'x', work: { id: 'a' } }], 'a')).toBeNull()
  })
})

describe('moveItem', () => {
  it('swaps neighbours and is a no-op at the ends', () => {
    expect(moveItem(['a', 'b', 'c'], 1, -1)).toEqual(['b', 'a', 'c'])
    expect(moveItem(['a', 'b', 'c'], 1, 1)).toEqual(['a', 'c', 'b'])
    const arr = ['a', 'b']
    expect(moveItem(arr, 0, -1)).toBe(arr)
    expect(moveItem(arr, 1, 1)).toBe(arr)
  })
})

describe('emptyRecapsDraft', () => {
  it('starts with a single blank entry and blank summaries', () => {
    const d = emptyRecapsDraft()
    expect(d.entries.length).toBe(1)
    expect(d.inShort).toBe('')
    expect(d.ending).toBe('')
  })
})
