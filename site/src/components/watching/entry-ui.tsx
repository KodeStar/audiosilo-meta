// The atoms the series page and the watching page both render for a series
// entry: when it came out, whether it is still a preorder, the "I have this"
// mark and the "not interested" one. Here rather than in either page because
// they must read identically on both - a reader ticks a box on one and looks
// for the result on the other.

import { formatReleaseDate, isFutureRelease } from '../../lib/dates'

/** The uppercase pill marking an entry whose release date is still ahead. Tinted
    rather than the neutral ui.tsx Badge, because it is the one thing on a row
    that changes what the reader can do about it. */
export function PreorderPill() {
  return (
    <span className="shrink-0 rounded-full border border-pink-500/40 bg-pink-600/10 px-2 py-0.5 text-[0.65rem] uppercase tracking-wide text-pink-300">
      Preorder
    </span>
  )
}

/** The "New" pill: this entry has not been listed for the reader before. */
export function NewPill() {
  return (
    <span className="shrink-0 rounded-full bg-pink-600 px-2 py-0.5 text-[0.65rem] font-semibold uppercase tracking-wide text-white">
      New
    </span>
  )
}

/** An entry's release date, at the precision the catalogue states, with the
    preorder pill when it is still ahead of `today`. Renders nothing at all when
    no recording of the work states a date - a placeholder would be a claim the
    data does not make. */
export function ReleaseLine({ date, today }: { date?: string | null; today: string }) {
  const formatted = formatReleaseDate(date)
  if (!formatted) return null
  return (
    <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-dim">
      <span>{formatted}</span>
      {isFutureRelease(date, today) ? <PreorderPill /> : null}
    </p>
  )
}

/** The chrome the two per-entry marks share - same size, same shape, same
    transition - so a row's "I have this" and "Not interested" read as a pair.
    Only the COLOURS differ, and only when the skip button is pressed, which is
    why they are a second string rather than part of this one. */
const MARK_CHROME = 'shrink-0 rounded-lg border px-3 py-2 text-xs transition-colors'
const MARK_IDLE = 'border-edge bg-raised text-dim hover:border-pink-500/50 hover:text-hi'

/** The per-entry ownership mark. The visible words are decorative (they repeat
    down a list); the input carries the work's title as its accessible name, so
    a screen reader hears which book each checkbox is about. */
export function OwnCheckbox({
  title,
  checked,
  onChange,
}: {
  title: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className={`${MARK_CHROME} ${MARK_IDLE} flex cursor-pointer items-center gap-2`}>
      <input
        type="checkbox"
        className="h-4 w-4 accent-pink-500"
        checked={checked}
        aria-label={`I have ${title}`}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span aria-hidden="true" className="hidden sm:inline">
        I have this
      </span>
    </label>
  )
}

/**
 * The per-entry "not interested" mark: a reader dismissing a series' novellas,
 * companion volumes or a spin-off they do not intend to read, WITHOUT claiming
 * to own them - which is what makes it a second control rather than a use of
 * the ownership checkbox beside it.
 *
 * It is PAGE-SIDE ONLY. A feed URL carries series slugs and nothing else
 * (lib/feed-url.ts), so the server cannot know about a skip and a skipped work
 * still arrives in the reader's Atom, JSON and calendar feeds. Reversible from
 * either view: the flat list and the series panels both render it, and a
 * skipped entry keeps the button in its "Show again" state.
 *
 * The visible words repeat down a list, so the button carries the work's title
 * in its accessible name and `aria-pressed` carries the state.
 */
export function SkipButton({
  title,
  skipped,
  onChange,
}: {
  title: string
  skipped: boolean
  onChange: (skipped: boolean) => void
}) {
  return (
    <button
      type="button"
      aria-pressed={skipped}
      aria-label={skipped ? `Show ${title} again` : `Not interested in ${title}`}
      onClick={() => onChange(!skipped)}
      className={`${MARK_CHROME} focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 ${
        skipped ? 'border-pink-500/50 bg-raised text-pink-300 hover:text-pink-200' : MARK_IDLE
      }`}
    >
      {skipped ? 'Show again' : 'Not interested'}
    </button>
  )
}
