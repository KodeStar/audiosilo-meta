// The three atoms the series page and the watching page both render for a
// series entry: when it came out, whether it is still a preorder, and the
// "I have this" mark. Here rather than in either page because they must read
// identically on both - a reader ticks a box on one and looks for the result on
// the other.

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
    <label className="flex shrink-0 cursor-pointer items-center gap-2 rounded-lg border border-edge bg-raised px-3 py-2 text-xs text-dim transition-colors hover:border-pink-500/50 hover:text-hi">
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
