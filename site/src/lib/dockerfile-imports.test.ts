import { describe, expect, it } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

// --- The cross-boundary imports' release-time guard --------------------------
//
// A few site modules import files from OUTSIDE site/ so the repo holds exactly
// one copy of each (the Go importer's genre table, the OpenAPI spec the docs
// page renders). The image's site stage has a build context of site/ ALONE, so
// every such file has to be COPYed to the matching position or `yarn build`
// fails to resolve the import - and that only happens at release time, so
// adding a third cross-boundary import can pass PR CI green and break the image
// build.
//
// This test derives the expected COPY set from the site sources themselves:
// every relative import specifier with enough `../` to leave site/, resolved to
// its repo path. A new one is therefore covered the moment it is written,
// which is the whole purpose of the guard.

const libDir = dirname(fileURLToPath(new URL('./dockerfile-imports.test.ts', import.meta.url)))
const siteRoot = resolve(libDir, '..', '..')
const repoRoot = resolve(siteRoot, '..')

/** The extensions whose import statements can reach across the boundary. */
const SOURCE_EXT = /\.(ts|tsx|astro|mts|js|jsx|mjs)$/

/** Directories with no authored source in them. */
const SKIP_DIRS = new Set(['node_modules', 'dist', '.astro'])

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue
    const path = resolve(dir, entry)
    if (statSync(path).isDirectory()) out.push(...sourceFiles(path))
    else if (SOURCE_EXT.test(entry)) out.push(path)
  }
  return out
}

/**
 * Every relative specifier in the file, from `import ... from '...'`, a bare
 * side-effect `import '...'`, or a dynamic `import('...')`. Astro frontmatter is
 * TypeScript, so one pattern covers every extension.
 */
function relativeSpecifiers(source: string): string[] {
  const out: string[] = []
  const re = /\bimport\s*(?:[\s\S]*?\bfrom\s*)?[('"\s]*(\.[^'"]*)['"]/g
  for (let m = re.exec(source); m !== null; m = re.exec(source)) out.push(m[1])
  return out
}

/** posix repo-relative path, so the comparison matches the Dockerfile's text. */
function repoRel(from: string, specifier: string): string {
  return relative(repoRoot, resolve(dirname(from), specifier)).split(sep).join('/')
}

/** The COPY lines and the `yarn build` line of the Dockerfile's site stage. */
function siteStage(): string[] {
  const lines = readFileSync(resolve(repoRoot, 'Dockerfile'), 'utf8').split('\n')
  const start = lines.findIndex((l) => /^\s*FROM\s+.*\bAS\s+site\s*$/i.test(l))
  expect(start, 'the Dockerfile has no stage named "site"').toBeGreaterThanOrEqual(0)
  const rest = lines.slice(start + 1)
  const end = rest.findIndex((l) => /^\s*FROM\s/i.test(l))
  return end === -1 ? rest : rest.slice(0, end)
}

describe('the Dockerfile site stage', () => {
  const escaping = new Set<string>()
  for (const file of sourceFiles(resolve(siteRoot, 'src'))) {
    for (const specifier of relativeSpecifiers(readFileSync(file, 'utf8'))) {
      const target = repoRel(file, specifier)
      // A path that stays inside site/ needs no COPY - the stage already has it.
      if (!target.startsWith('site/')) escaping.add(target)
    }
  }
  const expected = [...escaping].sort()

  it('has cross-boundary imports to guard', () => {
    // A zero-length list would make every assertion below vacuously true, so the
    // guard would read as coverage while checking nothing. If the last such
    // import ever goes away, delete this file and the Dockerfile's COPY lines
    // together.
    expect(expected.length).toBeGreaterThan(0)
  })

  it.each(expected)('copies %s to the position its import resolves to', (target) => {
    // The stage copies site/ to /site, so a path N levels above site/ sits at
    // the same repo-relative position under the container root.
    const want = { src: target, dest: `/${target}` }
    const stage = siteStage()

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
