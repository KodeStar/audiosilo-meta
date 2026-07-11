// Typed client for the audiosilo-meta API (the Go server behind
// meta.audiosilo.app). The server serves this built site same-origin, so the
// base URL defaults to "" (relative /api/... requests). For local development
// against a separate API process, set PUBLIC_API_BASE (see site/README.md).
//
// Every shipped island imports from HERE - never fetch() an endpoint inline, so
// the wire shapes live in one auditable place (mirrors the workspace's
// hand-mirrored contract rule).

const RAW_BASE = import.meta.env.PUBLIC_API_BASE ?? ''
// Trim a trailing slash so `${BASE}/api/...` never doubles up.
export const API_BASE = RAW_BASE.replace(/\/$/, '')

// --- Wire shapes ----------------------------------------------------------

export interface PersonRef {
  id: string
  name: string
}

export interface SeriesRef {
  id: string
  name: string
  position?: string
}

/** The compact card shape returned by search, latest, and person/series lists. */
export interface WorkCard {
  id: string
  title: string
  authors: PersonRef[]
  series?: SeriesRef | null
  cover_url?: string | null
  added_at?: string | null
  narrators?: PersonRef[]
}

export interface Stats {
  works: number
  recordings: number
  people: number
  series: number
  total_runtime_min: number
  total_chapters: number
  built_at: string
}

export type SearchResult =
  | {
      kind: 'work'
      id: string
      title: string
      authors: PersonRef[]
      series?: SeriesRef | null
      cover_url?: string | null
      narrators?: PersonRef[]
    }
  | { kind: 'person'; id: string; name: string }
  | { kind: 'series'; id: string; name: string; works: number }

export interface SearchResponse {
  results: SearchResult[]
}

export interface AsinRef {
  region: string
  asin: string
}

export interface Recording {
  id: string
  narrators: PersonRef[]
  abridged?: boolean
  runtime_min?: number
  release_date?: string
  publisher?: string
  asin?: AsinRef[]
  isbn?: string[]
  cover_url?: string | null
  chapter_count?: number
}

/** Cross-references to other open databases (the data model's work xrefs).
    All optional - rendered only when present. */
export interface WorkXrefs {
  wikidata?: string
  openlibrary?: string
  goodreads?: string
}

export interface Work {
  id: string
  title: string
  subtitle?: string
  authors: PersonRef[]
  language?: string
  first_published?: string
  description?: string
  series?: SeriesRef[]
  recordings: Recording[]
  /** Print ISBNs attached to the work itself (not a recording). */
  isbn?: string[]
  xrefs?: WorkXrefs
}

export interface Chapter {
  title: string
  start_ms: number
  length_ms: number
}

export interface ChaptersResponse {
  chapters: Chapter[]
}

export interface Person {
  id: string
  name: string
  sort_name?: string
  authored: WorkCard[]
  narrated: { work: WorkCard; recording_id: string }[]
}

export interface Series {
  id: string
  name: string
  authors: PersonRef[]
  works: { position: string; work: WorkCard }[]
}

export interface LookupResponse {
  work: WorkCard
  recording_id: string
}

// --- Fetch helpers --------------------------------------------------------

/** Thrown for any non-OK response so callers can distinguish 404 from failure. */
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    signal,
    headers: { Accept: 'application/json' },
  })
  if (!res.ok) {
    throw new ApiError(res.status, `Request failed: ${res.status}`)
  }
  return (await res.json()) as T
}

export function getStats(signal?: AbortSignal): Promise<Stats> {
  return getJSON<Stats>('/api/v1/stats', signal)
}

export function search(
  q: string,
  limit = 20,
  signal?: AbortSignal
): Promise<SearchResponse> {
  const params = new URLSearchParams({ q, limit: String(limit) })
  return getJSON<SearchResponse>(`/api/v1/search?${params.toString()}`, signal)
}

export function getLatestWorks(
  limit = 12,
  signal?: AbortSignal
): Promise<{ works: WorkCard[] }> {
  const params = new URLSearchParams({ limit: String(limit) })
  return getJSON<{ works: WorkCard[] }>(
    `/api/v1/works/latest?${params.toString()}`,
    signal
  )
}

export function getWork(id: string, signal?: AbortSignal): Promise<Work> {
  return getJSON<Work>(`/api/v1/works/${encodeURIComponent(id)}`, signal)
}

export function getChapters(
  workId: string,
  recordingId: string,
  signal?: AbortSignal
): Promise<ChaptersResponse> {
  return getJSON<ChaptersResponse>(
    `/api/v1/works/${encodeURIComponent(workId)}/recordings/${encodeURIComponent(
      recordingId
    )}/chapters`,
    signal
  )
}

export function getPerson(id: string, signal?: AbortSignal): Promise<Person> {
  return getJSON<Person>(`/api/v1/people/${encodeURIComponent(id)}`, signal)
}

export function getSeries(id: string, signal?: AbortSignal): Promise<Series> {
  return getJSON<Series>(`/api/v1/series/${encodeURIComponent(id)}`, signal)
}

/** Exact ASIN/ISBN lookup. Returns null on a 404 (no exact match). */
export async function lookup(
  kind: 'asin' | 'isbn',
  value: string,
  signal?: AbortSignal
): Promise<LookupResponse | null> {
  try {
    const params = new URLSearchParams({ [kind]: value })
    return await getJSON<LookupResponse>(
      `/api/v1/lookup?${params.toString()}`,
      signal
    )
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null
    throw err
  }
}

// --- Formatting + href helpers (shared by every card/detail view) ---------

/** "16h 36m" / "45m" from a runtime in minutes. */
export function formatRuntime(min?: number): string | null {
  if (!min || min <= 0) return null
  const h = Math.floor(min / 60)
  const m = Math.round(min % 60)
  if (h === 0) return `${m}m`
  if (m === 0) return `${h}h`
  return `${h}h ${m}m`
}

/** "mm:ss" or "h:mm:ss" from a millisecond offset (chapter start). */
export function formatOffset(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

/** A human year from an ISO-ish date string ("2021-05-04" -> "2021"). */
export function formatYear(date?: string | null): string | null {
  if (!date) return null
  const m = /^(\d{4})/.exec(date)
  return m ? m[1] : date
}

/** A readable language name from a BCP-47 code ("en" -> "English") via
    Intl.DisplayNames, falling back to the raw value when it is not a valid
    code (older data may already carry a display name). */
export function formatLanguage(code?: string | null): string | null {
  if (!code) return null
  try {
    const name = new Intl.DisplayNames(['en'], { type: 'language' }).of(code)
    return name || code
  } catch {
    return code
  }
}

export const href = {
  work: (id: string) => `/work?id=${encodeURIComponent(id)}`,
  person: (id: string) => `/person?id=${encodeURIComponent(id)}`,
  series: (id: string) => `/series?id=${encodeURIComponent(id)}`,
}

/** Detect a bare ASIN (Audible product code): 10 chars, starts with B0. */
export function looksLikeAsin(q: string): boolean {
  return /^B0[A-Za-z0-9]{8}$/.test(q.trim())
}

/** Detect an ISBN-10 or ISBN-13 (hyphens/spaces allowed). */
export function looksLikeIsbn(q: string): boolean {
  const digits = q.replace(/[\s-]/g, '')
  return /^(\d{9}[\dXx]|\d{13})$/.test(digits)
}

/** Normalised ISBN (strip separators) for the lookup query. */
export function normaliseIsbn(q: string): string {
  return q.replace(/[\s-]/g, '')
}
