// Which languages a reader wants to see, and every rule that turns that wish
// into an API parameter, a badge or a one-line suggestion. Framework-free and
// unit-tested, on the precedent of lib/marketplace.ts and lib/watchlist.ts: the
// header selector, the suggestion prompt and the islands that refetch are DOM
// glue over these functions.
//
// THE DEFAULT IS ALL LANGUAGES. Nothing here ever narrows what a reader sees on
// a guess: the browser's languages are only ever a SUGGESTION (suggestion()
// below, rendered as a dismissible prompt the reader answers), and a filter is
// applied only once the reader has chosen one - in the header selector, in that
// prompt, or by opening a shared link that names one (`?lang=de`), which applies
// to that view alone and is never written to storage on its own.
//
// A guess MAY decide a badge, because a badge hides nothing (needsBadge).
//
// MATCHING IS BY PRIMARY SUBTAG, as the server's `lang` parameter matches (RFC
// 4647 basic filtering on the primary subtag): choosing German keeps `de` and
// `de-at` alike, so every list here is held as bare primary subtags.

import type { LanguageCount } from './api'

/** The one localStorage key for the preference. Namespaced, because the site
    shares an origin with the API. */
export const LANGUAGES_STORAGE_KEY = 'audiosilo-meta:languages'

/** Dispatched on `window` after the preference is written, so every island on
    the page refetches at once (the `storage` event covers other tabs). The
    watch badge's WATCHLIST_CHANGED_EVENT is the precedent. */
export const LANGUAGES_CHANGED_EVENT = 'audiosilo-meta:languages-changed'

/** The URL parameter a shared link names a filter with - the API's own name, so
    a site URL and an API URL read the same. */
export const LANG_PARAM = 'lang'

/** The most languages one filter may name. The server refuses more than eight
    with a 400, so the site never builds a request it would refuse. */
export const MAX_LANGUAGES = 8

/** The stored preference. `languages` empty means ALL languages, chosen - which
    is also what a "No thanks" to the suggestion prompt stores. The document's
    ABSENCE means the reader has never said anything, which is the one state the
    suggestion prompt may appear in: its existence IS the answer, so the prompt
    never reappears after any choice. */
export interface LanguagePrefs {
  version: 1
  languages: string[]
}

// The schema's tag pattern (common.schema.json), lowercased: a 2-3 letter
// language and optional subtags. A tag outside it is not a language this
// catalogue can hold, so it is dropped rather than sent.
const TAG = /^[a-z]{2,3}(-[a-z0-9]{2,8})*$/

/** A tag's primary subtag ("de-AT" -> "de", "pt_BR" -> "pt"), or '' when the
    tag is not a language tag at all. Browsers really do report malformed tags,
    and underscores, so this normalizes rather than trusting. */
export function primarySubtag(tag: string | null | undefined): string {
  if (!tag) return ''
  const t = tag.trim().toLowerCase().replace(/_/g, '-')
  return TAG.test(t) ? t.split('-')[0] : ''
}

/** Primary subtags, deduplicated, invalid ones dropped, in the order given. */
function primaries(tags: readonly string[] | null | undefined): string[] {
  const out: string[] = []
  for (const tag of tags ?? []) {
    const p = primarySubtag(tag)
    if (p && !out.includes(p)) out.push(p)
  }
  return out
}

/** The canonical form of a filter: primary subtags, deduplicated, SORTED and
    capped at MAX_LANGUAGES - the server's own normalization, so one set of
    languages has one spelling on the wire and in storage. */
export function normalizeLanguages(tags: readonly string[]): string[] {
  return primaries(tags).sort().slice(0, MAX_LANGUAGES)
}

/** The reader's languages from `navigator.languages`, reduced to primary
    subtags in their own preference order ("de-DE, de, en-US" -> de, en). */
export const browserLanguages = primaries

/** The `lang` parameter value for a filter, '' for none (the caller then sends
    no parameter at all, so an unfiltered request is the request it always was). */
export function toParam(languages: readonly string[]): string {
  return normalizeLanguages(languages).join(',')
}

/** A `lang` value read back from a URL. Tolerant where the server is strict: an
    item that is not a language tag is dropped instead of producing a request
    the server would answer with a 400, since the value arrived in somebody
    else's link. */
export function fromParam(raw: string | null | undefined): string[] {
  if (!raw) return []
  return normalizeLanguages(raw.split(','))
}

/** The filter a shared link names (`?lang=de`), or null when the URL names
    none. An EMPTY or wholly-invalid value is also null - it names no filter,
    so it cannot override the reader's own preference. */
export function languagesFromSearch(search: string): string[] | null {
  const raw = new URLSearchParams(search).get(LANG_PARAM)
  const langs = fromParam(raw)
  return langs.length > 0 ? langs : null
}

/** The filter this view applies: a shared link's, else the stored preference,
    else none (all languages). */
export function activeLanguages(search: string, prefs: LanguagePrefs | null): string[] {
  return languagesFromSearch(search) ?? prefs?.languages ?? []
}

/** The languages a BADGE is judged against: the active filter when there is
    one, else the browser's own languages. A guess is allowed here, and only
    here, because a badge hides nothing. */
export function badgeContext(active: readonly string[], browser: readonly string[]): string[] {
  return active.length > 0 ? [...active] : [...browser]
}

/** Whether an item in `language` gets a language chip: its primary subtag is
    known and outside the context. No context (nothing chosen, no browser
    languages) or an unknown language means no chip - a chip claims a
    difference, and there is nothing to differ from. */
export function needsBadge(language: string | null | undefined, context: readonly string[]): boolean {
  const p = primarySubtag(language)
  if (!p || context.length === 0) return false
  return !context.includes(p)
}

/** One entry of the selector's list: a primary subtag and the works stating it
    (every regional tag folded in). */
export interface LanguageOption {
  language: string
  works: number
}

/** The /stats census grouped by primary subtag, most works first, then by
    tag - the order the selector lists them in. Absent (an artifact before
    schema_version 7) is an empty census. */
export function censusOptions(census: readonly LanguageCount[] | null | undefined): LanguageOption[] {
  const sums = new Map<string, number>()
  for (const row of census ?? []) {
    const p = primarySubtag(row.language)
    if (!p) continue
    sums.set(p, (sums.get(p) ?? 0) + (row.works || 0))
  }
  return [...sums.entries()]
    .map(([language, works]) => ({ language, works }))
    .sort((a, b) => b.works - a.works || a.language.localeCompare(b.language))
}

/** Whether the suggestion prompt may be asked at all, before the census is
    fetched: only while nothing is stored (no choice, no "No thanks"), not on a
    shared link that already names a filter, and only when the browser states a
    language other than English. Cheap on purpose - a reader it rules out costs
    no request. */
export function promptEligible(
  prefs: LanguagePrefs | null,
  fromUrl: boolean,
  browser: readonly string[]
): boolean {
  return prefs === null && !fromUrl && browser.some((l) => l !== 'en')
}

/** The languages the suggestion prompt offers: the browser's languages OTHER
    than English that the catalogue holds, in the browser's order. Empty -
    meaning no prompt - when the browser states only English, or when the
    catalogue holds none of them. English is left out because nearly the whole
    catalogue is English, so "show only English?" would be offering to hide
    almost nothing from a reader who did not ask. (Whether the prompt is asked
    at all is promptEligible's question.) */
export function suggestion(browser: readonly string[], census: readonly LanguageOption[]): string[] {
  const held = new Set(census.filter((c) => c.works > 0).map((c) => c.language))
  return primaries(browser)
    .filter((l) => l !== 'en' && held.has(l))
    .slice(0, MAX_LANGUAGES - 1)
}

/** The prompt's second choice: the suggested languages plus English, which is
    where the rest of the catalogue is. */
export function withEnglish(languages: readonly string[]): string[] {
  return normalizeLanguages([...languages, 'en'])
}

// Display names are asked for on every render of every chip and selector row,
// so each Intl.DisplayNames is constructed once (lazily, since a runtime without
// Intl.DisplayNames must still render) and each code's answer is memoized.
let englishNames: Intl.DisplayNames | null | undefined
const englishMemo = new Map<string, string>()
const nativeMemo = new Map<string, string>()

function englishDisplayNames(): Intl.DisplayNames | null {
  if (englishNames === undefined) {
    try {
      englishNames = new Intl.DisplayNames(['en'], { type: 'language' })
    } catch {
      englishNames = null
    }
  }
  return englishNames
}

/** A language's name in English ("de" -> "German"), the selector's label and
    the one implementation behind api.ts's formatLanguage. Falls back to the
    code itself where Intl cannot name it (an invalid code, or older data that
    already carries a display name). */
export function languageName(code: string): string {
  let name = englishMemo.get(code)
  if (name === undefined) {
    try {
      name = englishDisplayNames()?.of(code) || code
    } catch {
      name = code
    }
    englishMemo.set(code, name)
  }
  return name
}

/** A language's name in ITSELF ("de" -> "Deutsch", "fr" -> "Français"),
    capitalized, for the suggestion prompt: a German reader is asked about
    Deutsch. Falls back to the English name. */
export function nativeLanguageName(code: string): string {
  let name = nativeMemo.get(code)
  if (name === undefined) {
    try {
      const own = new Intl.DisplayNames([code], { type: 'language' }).of(code)
      name =
        !own || own === code
          ? languageName(code)
          : own.charAt(0).toLocaleUpperCase(code) + own.slice(1)
    } catch {
      name = languageName(code)
    }
    nativeMemo.set(code, name)
  }
  return name
}

/** "A", "A and B", "A, B and C" - the one list-joining rule the prompt and the
    selector's summary share. */
export function joinNames(names: readonly string[]): string {
  if (names.length <= 1) return names[0] ?? ''
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}

/** The selector button's short summary: "All", one language's name, or a
    count ("3 languages"). */
export function selectorSummary(languages: readonly string[]): string {
  if (languages.length === 0) return 'All'
  if (languages.length === 1) return languageName(languages[0])
  return `${languages.length} languages`
}

/** Parse a stored document, tolerating anything: a malformed or foreign value
    is "nothing stored" rather than an exception, fields it does not know are
    ignored, and the languages it names are normalized as if typed. */
export function parsePrefs(raw: string | null): LanguagePrefs | null {
  if (!raw) return null
  try {
    const doc = JSON.parse(raw) as Partial<LanguagePrefs> | null
    if (!doc || typeof doc !== 'object' || doc.version !== 1) return null
    const langs = Array.isArray(doc.languages)
      ? doc.languages.filter((l): l is string => typeof l === 'string')
      : []
    return { version: 1, languages: normalizeLanguages(langs) }
  } catch {
    return null
  }
}

/** A preference document for an explicit choice. Storing any choice dismisses
    the prompt: a reader who has answered the question is not asked it again. */
export function chosenPrefs(languages: readonly string[]): LanguagePrefs {
  return { version: 1, languages: normalizeLanguages(languages) }
}

/** The stored preference, or null. Every failure - private mode, blocked
    storage, no localStorage at all - degrades to "nothing stored", so a reader
    with storage off still gets the whole catalogue. */
export function readLanguagePrefs(): LanguagePrefs | null {
  try {
    return parsePrefs(globalThis.localStorage?.getItem(LANGUAGES_STORAGE_KEY) ?? null)
  } catch {
    return null
  }
}

/** Remember a choice. Silent on failure: the choice still applies to the page
    in front of the reader (the caller dispatches the change event either way),
    it just will not outlive it. */
export function writeLanguagePrefs(prefs: LanguagePrefs): void {
  try {
    globalThis.localStorage?.setItem(LANGUAGES_STORAGE_KEY, JSON.stringify(prefs))
  } catch {
    /* storage blocked - the choice still applies for this page load */
  }
}
