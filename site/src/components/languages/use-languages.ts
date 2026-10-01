// The DOM glue between lib/languages.ts (every rule, pure and tested) and the
// islands that read the reader's language filter: one hook that keeps an island
// in step with the preference, one action that changes it, and one memoized
// read of the catalogue's language census.
//
// Islands are separate React roots, so they cannot share state through React.
// They share it the way the header's watch badge does: the preference lives in
// localStorage, a change made in this tab dispatches LANGUAGES_CHANGED_EVENT on
// window, and the `storage` event carries a change made in another tab. Every
// listener RE-READS rather than trusting an event payload, so a page can never
// hold two opinions about which filter is on.

import { useEffect, useState } from 'react'
import { getStats } from '../../lib/api'
import {
  LANGUAGES_CHANGED_EVENT,
  LANGUAGES_STORAGE_KEY,
  LANG_PARAM,
  activeLanguages,
  badgeContext,
  browserLanguages,
  censusOptions,
  chosenPrefs,
  languagesFromSearch,
  readLanguagePrefs,
  toParam,
  writeLanguagePrefs,
  type LanguageOption,
  type LanguagePrefs,
} from '../../lib/languages'

export interface LanguageState {
  /** False until the island has mounted and read the browser. The prerendered
      page knows no reader, so an island waits for this before its first
      request - otherwise it would fetch unfiltered and then filtered. */
  ready: boolean
  /** The filter this view applies (empty = all languages). */
  active: string[]
  /** `active` as the canonical `lang` value - a stable dependency for effects. */
  key: string
  /** True when `active` came from a shared link's `?lang=` rather than from the
      reader's own stored choice. */
  fromUrl: boolean
  /** What a language chip is judged against (lib/languages.ts badgeContext). */
  context: string[]
  /** The stored preference, null when the reader has never chosen. */
  prefs: LanguagePrefs | null
  /** The browser's own languages, as primary subtags. */
  browser: string[]
}

const INITIAL: LanguageState = {
  ready: false,
  active: [],
  key: '',
  fromUrl: false,
  context: [],
  prefs: null,
  browser: [],
}

function readState(): LanguageState {
  const prefs = readLanguagePrefs()
  const search = window.location.search
  const active = activeLanguages(search, prefs)
  const browser = browserLanguages(navigator.languages ?? [navigator.language])
  return {
    ready: true,
    active,
    key: toParam(active),
    fromUrl: languagesFromSearch(search) !== null,
    context: badgeContext(active, browser),
    prefs,
    browser,
  }
}

/** The reader's language filter, kept current across islands and tabs. */
export function useLanguages(): LanguageState {
  const [state, setState] = useState<LanguageState>(INITIAL)
  useEffect(() => {
    const refresh = () => setState(readState())
    refresh()
    const onStorage = (e: StorageEvent) => {
      // A null key is a whole-origin clear.
      if (e.key === null || e.key === LANGUAGES_STORAGE_KEY) refresh()
    }
    window.addEventListener(LANGUAGES_CHANGED_EVENT, refresh)
    window.addEventListener('storage', onStorage)
    return () => {
      window.removeEventListener(LANGUAGES_CHANGED_EVENT, refresh)
      window.removeEventListener('storage', onStorage)
    }
  }, [])
  return state
}

/** Save the reader's explicit choice and tell every island. A shared link's
    `?lang=` stops applying the moment the reader chooses for themselves, so it
    is taken out of the address bar too - otherwise the URL would keep naming a
    filter the page no longer shows, and a reload would bring it back. Nothing
    else in the URL is touched. */
export function chooseLanguages(languages: readonly string[]): void {
  writeLanguagePrefs(chosenPrefs(languages))
  clearUrlLanguages()
  window.dispatchEvent(new CustomEvent(LANGUAGES_CHANGED_EVENT))
}

/** Record a "No thanks" to the suggestion prompt: all languages, and do not
    ask again. */
export function dismissSuggestion(): void {
  chooseLanguages([])
}

function clearUrlLanguages(): void {
  const url = new URL(window.location.href)
  if (!url.searchParams.has(LANG_PARAM)) return
  url.searchParams.delete(LANG_PARAM)
  window.history.replaceState(window.history.state, '', url)
}

let censusMemo: Promise<LanguageOption[]> | null = null

/** The catalogue's languages, from `/stats`, fetched at most once per page
    whichever islands ask (the selector, the prompt). A failure resolves to an
    empty census - the selector says the list is unavailable, the prompt stays
    away - and is not memoized, so a later ask may succeed. */
export function loadLanguageCensus(): Promise<LanguageOption[]> {
  if (!censusMemo) {
    censusMemo = getStats()
      .then((stats) => censusOptions(stats.languages))
      .catch(() => {
        censusMemo = null
        return []
      })
  }
  return censusMemo
}
