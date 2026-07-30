import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

import { mapGenreClaim, mapGenreClaims } from './audible-genres'

// The table the Go importer embeds - read from disk here so the assertions below
// are made against the same file the module imports, and so the fixtures used in
// the tests are provably real entries rather than invented ones.
const table = JSON.parse(
  readFileSync(new URL('../../../internal/importer/audiblegenres.json', import.meta.url), 'utf8')
) as { by_asin: Record<string, string>; by_name: Record<string, string> }

// A browse-node id whose mapping differs from what its paired NAME would give,
// so the lookup ORDER is what the assertion proves.
const NODE_ID = '121478793011'
const NODE_GENRE = 'parenting-relationships'

describe('the mapping table', () => {
  it('is the file the Go importer embeds', () => {
    // A floor, not an exact count: the table grows. An empty half would make
    // every claim unmappable and the mapping silently a no-op.
    expect(Object.keys(table.by_name).length).toBeGreaterThan(1000)
    expect(Object.keys(table.by_asin).length).toBeGreaterThan(2000)
    expect(table.by_asin[NODE_ID]).toBe(NODE_GENRE)
    expect(table.by_name['crime thrillers']).toBe('thriller-suspense')
  })
})

describe('mapGenreClaim', () => {
  it('resolves a browse-node id', () => {
    expect(mapGenreClaim({ node: NODE_ID })).toBe(NODE_GENRE)
    expect(mapGenreClaim({ node: ` ${NODE_ID} ` })).toBe(NODE_GENRE)
  })

  it('resolves a display name, case- and whitespace-insensitively', () => {
    expect(mapGenreClaim({ name: 'Crime Thrillers' })).toBe('thriller-suspense')
    expect(mapGenreClaim({ name: '  crime thrillers  ' })).toBe('thriller-suspense')
  })

  it('prefers the node id over the name (mirrors the Go lookup order)', () => {
    // The name alone would map to epic-fantasy; the node wins.
    expect(mapGenreClaim({ name: 'epic' })).toBe('epic-fantasy')
    expect(mapGenreClaim({ node: NODE_ID, name: 'epic' })).toBe(NODE_GENRE)
  })

  it('falls back to the name when the node id is unknown', () => {
    expect(mapGenreClaim({ node: '000000000000', name: 'epic' })).toBe('epic-fantasy')
  })

  it('is undefined for a claim that does not map', () => {
    expect(mapGenreClaim({ name: 'Not A Real Category' })).toBeUndefined()
    expect(mapGenreClaim({ node: '000000000000' })).toBeUndefined()
    expect(mapGenreClaim({})).toBeUndefined()
    expect(mapGenreClaim({ name: '   ' })).toBeUndefined()
  })

  // The lookup key is data from a third-party mirror, so it can name anything.
  // With plain object literals as the maps, these keys resolve THROUGH
  // Object.prototype: 'constructor' returns the Object function and '__proto__'
  // returns a prototype object - neither is a vocabulary slug, both violate the
  // declared `string | undefined`, and both would ride into the confirmation
  // card and the prefilled issue URL. Null-prototype maps make them undefined.
  it.each(['constructor', '__proto__', 'toString', 'valueOf', 'hasOwnProperty'])(
    'does not resolve the prototype-chain key %s',
    (key) => {
      expect(mapGenreClaim({ node: key })).toBeUndefined()
      expect(mapGenreClaim({ name: key })).toBeUndefined()
      expect(mapGenreClaim({ node: key, name: key })).toBeUndefined()
    }
  )
})

describe('mapGenreClaims', () => {
  it('deduplicates and sorts ascending', () => {
    const got = mapGenreClaims([
      { name: 'epic' },
      { name: 'Crime Thrillers' },
      { node: NODE_ID, name: 'epic' },
      { name: 'crime thrillers' },
    ])
    expect(got).toEqual(['epic-fantasy', NODE_GENRE, 'thriller-suspense'].sort())
  })

  it('drops the claims that do not map, keeping the ones that do', () => {
    expect(mapGenreClaims([{ name: 'Not A Real Category' }, { name: 'epic' }])).toEqual([
      'epic-fantasy',
    ])
  })

  it('is empty for no claims', () => {
    expect(mapGenreClaims([])).toEqual([])
    expect(mapGenreClaims([{ name: 'Not A Real Category' }])).toEqual([])
  })

  it('drops prototype-chain keys instead of emitting a non-slug', () => {
    // Every element must still be a string from the vocabulary - a leaked
    // Object.prototype member would be a function or an object here.
    const got = mapGenreClaims([
      { name: 'constructor' },
      { node: 'constructor' },
      { name: '__proto__' },
      { node: '__proto__' },
      { name: 'Epic' },
    ])
    expect(got).toEqual(['epic-fantasy'])
    for (const slug of got) expect(typeof slug).toBe('string')
  })
})

// --- The cross-boundary import's release-time guard --------------------------

// audible-genres.ts imports the mapping table from OUTSIDE site/, so the image's
// site stage (whose build context is site/ alone) has to COPY that file to the
// matching relative position or `yarn build` fails to resolve the import. That
// only happens at release time, so moving or renaming the table can otherwise
// pass PR CI green and break the image build. This test ties the two together:
// it derives the expected COPY from the module's own import specifier.
describe('the Dockerfile site stage', () => {
  const libDir = dirname(fileURLToPath(new URL('./audible-genres.ts', import.meta.url)))
  const repoRoot = resolve(libDir, '..', '..', '..')
  const source = readFileSync(resolve(libDir, 'audible-genres.ts'), 'utf8')

  it('copies the table the module imports, to the position the import resolves to', () => {
    const match = source.match(/from\s+'((?:\.\.\/)+[^']*audiblegenres\.json)'/)
    if (!match) throw new Error('audible-genres.ts no longer imports audiblegenres.json by a relative path')
    const repoRel = relative(repoRoot, resolve(libDir, match[1])).split(sep).join('/')
    // A path inside site/ would need no COPY at all - if the table ever moves
    // there, delete the Dockerfile line and this test together.
    expect(repoRel.startsWith('site/')).toBe(false)
    // The site stage copies site/ to /site, so a path N levels above site/ sits
    // at the same repo-relative position under the container root.
    const want = { src: repoRel, dest: `/${repoRel}` }

    const dockerfile = readFileSync(resolve(repoRoot, 'Dockerfile'), 'utf8')
    const lines = dockerfile.split('\n')
    const start = lines.findIndex((l) => /^\s*FROM\s+.*\bAS\s+site\s*$/i.test(l))
    expect(start).toBeGreaterThanOrEqual(0)
    const rest = lines.slice(start + 1)
    const end = rest.findIndex((l) => /^\s*FROM\s/i.test(l))
    const stage = end === -1 ? rest : rest.slice(0, end)

    const copyAt = stage.findIndex((l) => {
      const m = l.match(/^\s*COPY\s+(\S+)\s+(\S+)\s*$/i)
      return m !== null && m[1] === want.src && m[2] === want.dest
    })
    expect(
      copyAt,
      `Dockerfile site stage is missing: COPY ${want.src} ${want.dest}`
    ).toBeGreaterThanOrEqual(0)

    // ... and it has to land before the build that resolves the import.
    const buildAt = stage.findIndex((l) => /^\s*RUN\s+.*\byarn\s+build\b/.test(l))
    expect(buildAt).toBeGreaterThanOrEqual(0)
    expect(copyAt).toBeLessThan(buildAt)
  })
})
