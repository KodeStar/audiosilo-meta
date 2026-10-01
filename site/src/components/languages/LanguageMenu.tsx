import { useEffect, useRef, useState } from 'react'
import {
  MAX_LANGUAGES,
  languageName,
  selectorSummary,
  type LanguageOption,
} from '../../lib/languages'
import { formatStat } from '../../lib/stat-size'
import { Icon } from '../ui'
import { chooseLanguages, loadLanguageCensus, useLanguages } from './use-languages'

interface Props {
  /** `bar`: the desktop header's compact button with a popover. `menu`: a
      full-width row in the phone menu that expands the list in place. The
      choices and their effect are identical. */
  variant?: 'bar' | 'menu'
}

/** What differs between the two variants - everything else is one markup path. */
interface VariantStyle {
  /** The list opens as a floating popover that closes on an outside click or
      Escape (bar), or expands in place inside the phone menu, which owns
      closing (menu). */
  popover: boolean
  wrapper: string
  trigger: string
  /** The trigger's text colour while a filter is on, and while none is. */
  filtered: string
  unfiltered: string
  icon: string
  list: string
}

const VARIANTS: Record<NonNullable<Props['variant']>, VariantStyle> = {
  bar: {
    popover: true,
    wrapper: 'relative',
    trigger:
      'flex items-center gap-1.5 rounded-md text-sm font-medium transition-colors hover:text-pink-500',
    filtered: 'text-pink-400',
    unfiltered: 'text-body',
    icon: 'h-5 w-5',
    list: 'p-2',
  },
  menu: {
    popover: false,
    wrapper: '',
    trigger:
      'flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm font-medium transition-colors hover:bg-raised hover:text-hi',
    filtered: 'text-body',
    unfiltered: 'text-body',
    icon: 'h-4 w-4',
    list: 'px-1 pb-2',
  },
}

/**
 * The header's language selector: "Languages: All" until the reader chooses.
 *
 * The list is the catalogue's own census (`/stats` languages, grouped by
 * primary subtag, most works first), so it only ever offers a language that
 * has books. Every change is saved at once and announced to the page's other
 * islands (use-languages.ts), which refetch - there is no Apply button to
 * forget. "All languages" clears the filter.
 */
export default function LanguageMenu({ variant = 'bar' }: Props) {
  const languages = useLanguages()
  const [open, setOpen] = useState(false)
  const [census, setCensus] = useState<LanguageOption[] | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const style = VARIANTS[variant]

  // The census is read on EVERY open, from the page's one shared /stats read
  // (also the suggestion prompt's; one request a page, whoever asks first). A
  // reopen after a success is answered from that memo, and since it remembers
  // only a success, a reopen after a transient failure asks again rather than
  // keeping "not available" for the rest of the page's life.
  useEffect(() => {
    if (!open) return
    let live = true
    void loadLanguageCensus().then((c) => {
      if (live) setCensus(c)
    })
    return () => {
      live = false
    }
  }, [open])

  // The popover closes on an outside click or Escape; the phone menu's inline
  // list stays put (the menu itself owns closing).
  useEffect(() => {
    if (!open || !style.popover) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        // Back to the button the popover hangs off, so keyboard focus is not
        // left on a checkbox that no longer exists.
        triggerRef.current?.focus()
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, style.popover])

  const active = languages.active
  const summary = selectorSummary(active)
  const label = `Languages: ${summary}`

  // A language the filter names but the census does not list (a shared link
  // naming one the catalogue lacks, a census not loaded) still gets a row, so
  // the reader can always see - and clear - what is applied.
  const options: LanguageOption[] = [...(census ?? [])]
  for (const l of active) {
    if (!options.some((o) => o.language === l)) options.push({ language: l, works: 0 })
  }

  const toggle = (language: string) => {
    const next = active.includes(language)
      ? active.filter((l) => l !== language)
      : [...active, language]
    chooseLanguages(next)
  }

  const list = (
    <div className={style.list}>
      {languages.fromUrl ? (
        <p className="mb-2 rounded-lg bg-raised px-3 py-2 text-xs leading-relaxed text-dim">
          Set by the link you opened. Changing it here saves your own choice.
        </p>
      ) : null}
      <ul className="max-h-72 overflow-y-auto" aria-label="Languages to show">
        <li>
          <label className="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-body hover:bg-raised hover:text-hi">
            <input
              type="radio"
              name={`languages-all-${variant}`}
              checked={active.length === 0}
              onChange={() => chooseLanguages([])}
              className="accent-pink-500"
            />
            <span className="font-medium">All languages</span>
          </label>
        </li>
        {census === null && open ? (
          <li className="px-3 py-2 text-xs text-dim">Loading languages...</li>
        ) : null}
        {census !== null && options.length === 0 ? (
          <li className="px-3 py-2 text-xs leading-relaxed text-dim">
            The language list is not available right now.
          </li>
        ) : null}
        {options.map((o) => {
          const checked = active.includes(o.language)
          const full = !checked && active.length >= MAX_LANGUAGES
          return (
            <li key={o.language}>
              <label
                className={`flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-body ${
                  full ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-raised hover:text-hi'
                }`}
              >
                <input
                  type="checkbox"
                  checked={checked}
                  disabled={full}
                  onChange={() => toggle(o.language)}
                  className="accent-pink-500"
                />
                <span className="min-w-0 flex-1 truncate">{languageName(o.language)}</span>
                {o.works > 0 ? (
                  <span className="shrink-0 text-xs tabular-nums text-dim">
                    {formatStat(o.works)}
                  </span>
                ) : null}
              </label>
            </li>
          )
        })}
      </ul>
      <a
        href="/docs/languages"
        className="mt-1 block rounded-lg px-3 py-2 text-xs text-pink-400 transition-colors hover:bg-raised hover:text-pink-300"
      >
        How language filtering works
      </a>
    </div>
  )

  return (
    <div ref={rootRef} className={style.wrapper}>
      <button
        ref={triggerRef}
        type="button"
        aria-expanded={open}
        aria-haspopup={style.popover ? 'true' : undefined}
        aria-label={label}
        title={label}
        onClick={() => setOpen((v) => !v)}
        className={`${style.trigger} ${active.length > 0 ? style.filtered : style.unfiltered}`}
      >
        <Icon name="language" className={style.icon} />
        {style.popover ? (
          <span className="max-w-[7rem] truncate">{summary}</span>
        ) : (
          <>
            <span className="flex-1">Languages</span>
            <span className="text-xs text-dim">{summary}</span>
          </>
        )}
      </button>
      {open && style.popover ? (
        <div className="absolute right-0 top-full z-50 mt-3 w-64 rounded-xl border border-edge bg-surface shadow-2xl shadow-black/40">
          <p className="border-b border-edge px-4 py-2.5 text-xs font-semibold uppercase tracking-[0.15em] text-dim">
            Show audiobooks in
          </p>
          {list}
        </div>
      ) : open ? (
        list
      ) : null}
    </div>
  )
}
