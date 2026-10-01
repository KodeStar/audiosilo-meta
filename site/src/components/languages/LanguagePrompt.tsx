import { useEffect, useState } from 'react'
import {
  joinNames,
  languageName,
  nativeLanguageName,
  promptEligible,
  suggestion,
  withEnglish,
} from '../../lib/languages'
import { Icon } from '../ui'
import { chooseLanguages, loadLanguageCensus, useLanguages } from './use-languages'

/**
 * The one-line suggestion under the header: "Show only Deutsch audiobooks?"
 *
 * It is a QUESTION, never an action - the browser's languages decide only
 * whether it is asked, and nothing is filtered until the reader answers. It
 * appears only while nothing is stored (no choice, no "No thanks"), when the
 * browser states a language other than English that the catalogue holds, and
 * not on a shared link that already names a filter. Any answer is stored, so
 * it is asked once.
 *
 * The census is only fetched once the cheap conditions hold, so a reader whose
 * browser states only English costs no request at all.
 */
export default function LanguagePrompt() {
  const { ready, prefs, fromUrl, browser } = useLanguages()
  const [offer, setOffer] = useState<string[]>([])

  const eligible = ready && promptEligible(prefs, fromUrl, browser)

  useEffect(() => {
    if (!eligible) {
      setOffer([])
      return
    }
    let live = true
    void loadLanguageCensus().then((census) => {
      if (live) setOffer(suggestion(browser, census))
    })
    return () => {
      live = false
    }
  }, [eligible, browser])

  if (!eligible || offer.length === 0) return null

  const native = joinNames(offer.map(nativeLanguageName))
  const english = languageName('en')

  const btn =
    'rounded-lg border px-3 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500'

  return (
    <aside
      aria-label="Language suggestion"
      /* relative + z-40: the page backdrops bleed upward under the header
         (Backdrop.astro's --hero-bleed), and would otherwise paint over this
         line; below the sticky header's z-50, so it scrolls away beneath it. */
      className="relative z-40 border-b border-edge bg-surface/90 backdrop-blur-md"
    >
      <div className="container flex flex-wrap items-center gap-x-4 gap-y-2 py-2.5 text-sm">
        <span className="flex items-center gap-2 text-body">
          <Icon name="language" className="h-4 w-4 shrink-0 text-pink-400" />
          <span>
            Show only <span className="font-medium text-hi">{native}</span> audiobooks?{' '}
            <a
              href="/docs/languages"
              className="text-xs text-dim underline-offset-2 hover:text-pink-300 hover:underline"
            >
              About languages
            </a>
          </span>
        </span>
        <span className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => chooseLanguages(offer)}
            className={`${btn} border-pink-600 bg-pink-600 text-white hover:bg-pink-500`}
          >
            Only {native}
          </button>
          <button
            type="button"
            onClick={() => chooseLanguages(withEnglish(offer))}
            className={`${btn} border-edge text-hi hover:border-pink-500`}
          >
            {native} and {english}
          </button>
          <button
            type="button"
            onClick={() => chooseLanguages([])}
            className={`${btn} border-transparent text-dim hover:text-hi`}
          >
            No thanks
          </button>
        </span>
      </div>
    </aside>
  )
}
