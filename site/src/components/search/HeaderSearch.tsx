import { useEffect, useRef, useState } from 'react'
import SearchBox from './SearchBox'

interface Props {
  /** True on the home page, whose hero already owns a full-size search box.
      There the header search stays hidden until the hero has scrolled away, so
      the two are never on screen competing at once. */
  deferToHero?: boolean
}

/**
 * The search that follows you around the site.
 *
 * Every page except home shows it immediately; home reveals it once the hero's
 * own box has scrolled out of reach. Below `sm` it collapses to an icon that
 * opens a full-width sheet, because a usable search field and the wordmark do
 * not both fit on a phone.
 */
export default function HeaderSearch({ deferToHero = false }: Props) {
  const [revealed, setRevealed] = useState(!deferToHero)
  const [sheetOpen, setSheetOpen] = useState(false)
  const sheetRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!deferToHero) return
    // Watch the hero's own search box rather than a scroll threshold, so the
    // handover happens exactly when the reader loses the thing it replaces -
    // at any viewport height, with no magic pixel number to drift.
    const target = document.querySelector('[data-hero-search]')
    if (!target) {
      setRevealed(true)
      return
    }
    const io = new IntersectionObserver(
      ([entry]) => setRevealed(!entry.isIntersecting),
      // The sticky header is ~4rem tall; treat the box as gone once it passes
      // under it rather than when it clears the top of the viewport.
      { rootMargin: '-72px 0px 0px 0px', threshold: 0 }
    )
    io.observe(target)
    return () => io.disconnect()
  }, [deferToHero])

  // Close the phone sheet on Escape or an outside click.
  useEffect(() => {
    if (!sheetOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setSheetOpen(false)
    }
    const onDown = (e: MouseEvent) => {
      if (sheetRef.current && !sheetRef.current.contains(e.target as Node)) setSheetOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onDown)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onDown)
    }
  }, [sheetOpen])

  return (
    <div
      className={`header-search transition-opacity duration-200 ${
        revealed ? 'opacity-100' : 'pointer-events-none opacity-0'
      }`}
      aria-hidden={!revealed}
      /* While hidden the field is still in the DOM, so it must also be out of
         the tab order - otherwise Tab lands the caret in an invisible box. */
      inert={!revealed}
    >
      {/* Tablet and up: the box lives in the bar. */}
      <div className="hidden w-full max-w-lg sm:block">
        <SearchBox compact />
      </div>

      {/* Phone: an icon that opens a full-width sheet under the bar. */}
      <div className="sm:hidden" ref={sheetRef}>
        <button
          type="button"
          aria-label="Search"
          aria-expanded={sheetOpen}
          onClick={() => setSheetOpen((v) => !v)}
          className="flex h-10 w-10 items-center justify-center rounded-md text-body transition-colors hover:text-hi"
        >
          <svg
            className="h-5 w-5"
            xmlns="http://www.w3.org/2000/svg"
            fill="none"
            viewBox="0 0 24 24"
            strokeWidth={1.5}
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z"
            />
          </svg>
        </button>
        {sheetOpen ? (
          <div className="absolute inset-x-0 top-full border-b border-edge bg-deep/95 p-3 backdrop-blur-md">
            <SearchBox compact autoFocus />
          </div>
        ) : null}
      </div>
    </div>
  )
}
