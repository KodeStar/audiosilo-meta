// Throwaway mock of the audiosilo-meta API for visual development ONLY.
// It is NOT shipped and NOT imported by any page - the site only ever talks to
// the typed client in src/lib/api.ts via PUBLIC_API_BASE. Run it, then point the
// dev server at it:
//
//   node mock/server.mjs                       # serves on http://localhost:8099
//   PUBLIC_API_BASE=http://localhost:8099 yarn dev
//
// It answers every /api/v1/* route in the contract from static fixtures and
// sends permissive CORS headers so the Vite dev origin can read it.
import { createServer } from 'node:http'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const db = JSON.parse(readFileSync(join(here, 'fixtures.json'), 'utf8'))
const PORT = Number(process.env.PORT ?? 8099)

function send(res, status, body) {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
  })
  res.end(JSON.stringify(body))
}

function cardOf(workId) {
  const w = db.works[workId]
  if (!w) return null
  return {
    id: w.id,
    title: w.title,
    authors: w.authors,
    series: w.series && w.series[0] ? w.series[0] : null,
    cover_url: (w.recordings.find((r) => r.cover_url) || {}).cover_url ?? null,
    added_at: w.added_at ?? null,
    narrators: w.recordings[0]?.narrators ?? [],
  }
}

const server = createServer((req, res) => {
  if (req.method === 'OPTIONS') return send(res, 204, {})
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const p = url.pathname

  if (p === '/api/v1/stats') return send(res, 200, db.stats)

  if (p === '/api/v1/search') {
    const q = (url.searchParams.get('q') || '').toLowerCase().trim()
    const results = []
    // Series and people are DELIBERATELY emitted before works: the real API
    // ranks across kinds (bm25), so kind order is not guaranteed - this makes
    // the mock exercise the UI's fixed group order (Works, People, Series).
    for (const s of Object.values(db.series)) {
      if (s.name.toLowerCase().includes(q))
        results.push({ kind: 'series', id: s.id, name: s.name, works: s.works.length })
    }
    for (const person of Object.values(db.people)) {
      if (person.name.toLowerCase().includes(q))
        results.push({ kind: 'person', id: person.id, name: person.name })
    }
    for (const w of Object.values(db.works)) {
      if (
        w.title.toLowerCase().includes(q) ||
        w.authors.some((a) => a.name.toLowerCase().includes(q)) ||
        w.recordings.some((r) => r.narrators.some((n) => n.name.toLowerCase().includes(q)))
      ) {
        results.push({
          kind: 'work',
          id: w.id,
          title: w.title,
          authors: w.authors,
          series: w.series && w.series[0] ? w.series[0] : null,
          cover_url: (w.recordings.find((r) => r.cover_url) || {}).cover_url ?? null,
          narrators: w.recordings[0]?.narrators ?? [],
        })
      }
    }
    return send(res, 200, { results: results.slice(0, Number(url.searchParams.get('limit') || 20)) })
  }

  if (p === '/api/v1/works/latest') {
    const works = Object.values(db.works).map((w) => cardOf(w.id))
    return send(res, 200, { works: works.slice(0, Number(url.searchParams.get('limit') || 12)) })
  }

  let m
  if ((m = p.match(/^\/api\/v1\/works\/([^/]+)\/recordings\/([^/]+)\/chapters$/))) {
    const w = db.works[m[1]]
    const r = w?.recordings.find((x) => x.id === m[2])
    if (!r) return send(res, 404, { error: 'not found' })
    return send(res, 200, { chapters: r.chapters ?? [] })
  }
  if ((m = p.match(/^\/api\/v1\/works\/([^/]+)$/))) {
    const w = db.works[m[1]]
    if (!w) return send(res, 404, { error: 'not found' })
    // Strip chapters from the recordings; those load lazily via the endpoint above.
    const recordings = w.recordings.map(({ chapters, ...rest }) => ({
      ...rest,
      chapter_count: rest.chapter_count ?? (chapters ? chapters.length : undefined),
    }))
    return send(res, 200, { ...w, recordings })
  }
  if ((m = p.match(/^\/api\/v1\/people\/([^/]+)$/))) {
    const person = db.people[m[1]]
    if (!person) return send(res, 404, { error: 'not found' })
    return send(res, 200, person)
  }
  if ((m = p.match(/^\/api\/v1\/series\/([^/]+)$/))) {
    const s = db.series[m[1]]
    if (!s) return send(res, 404, { error: 'not found' })
    return send(res, 200, s)
  }
  if (p === '/api/v1/lookup') {
    const asin = url.searchParams.get('asin')
    const isbn = url.searchParams.get('isbn')
    for (const w of Object.values(db.works)) {
      for (const r of w.recordings) {
        if (asin && (r.asin || []).some((a) => a.asin === asin))
          return send(res, 200, { work: cardOf(w.id), recording_id: r.id })
        if (isbn && (r.isbn || []).includes(isbn))
          return send(res, 200, { work: cardOf(w.id), recording_id: r.id })
      }
    }
    return send(res, 404, { error: 'not found' })
  }

  return send(res, 404, { error: 'no such route' })
})

server.listen(PORT, () => {
  console.log(`mock audiosilo-meta API on http://localhost:${PORT}`)
})
