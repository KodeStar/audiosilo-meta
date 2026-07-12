// Small presentational primitives shared by the /build editors. Kept out of the
// Astro component set because those (Icon.astro, Button.astro) cannot be used
// inside a React island; the classes are replicated from the site's components
// so the builder matches the rest of the design.

// Button classes replicated from Button.astro / ImportTool.
const BTN_BASE =
  'inline-flex items-center justify-center gap-2 rounded-lg px-6 py-3 font-medium transition-all duration-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:translate-y-0'
export const BTN_PRIMARY = `${BTN_BASE} bg-pink-600 text-white shadow-lg shadow-pink-600/20 hover:-translate-y-0.5 hover:bg-pink-500 hover:shadow-pink-500/30`
export const BTN_SECONDARY = `${BTN_BASE} border border-edge text-hi hover:-translate-y-0.5 hover:border-pink-500`

// Shared form-control classes.
export const INPUT =
  'w-full rounded-lg border border-edge bg-raised px-3 py-2 text-sm text-hi placeholder:text-dim/50 focus:border-pink-500 focus:outline-none'
export const TEXTAREA = `${INPUT} min-h-[6rem] leading-relaxed`

// The compact pill link used for inline CTAs (coverage rows, work-page CTAs).
// Padding is per call site, so row pills stay tighter than standalone CTAs.
export const PILL_LINK =
  'inline-flex items-center rounded-lg border border-edge text-sm font-medium text-hi transition-colors hover:border-pink-500 hover:text-pink-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500'

// Inline outline icons (heroicons paths), since Icon.astro cannot render inside
// a React island.
const ICON_PATHS = {
  download:
    'M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-6L12 15m0 0 4.5-4.5M12 15V3',
  external:
    'M13.5 6H5.25A2.25 2.25 0 0 0 3 8.25v10.5A2.25 2.25 0 0 0 5.25 21h10.5A2.25 2.25 0 0 0 18 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25',
  plus: 'M12 4.5v15m7.5-7.5h-15',
  trash:
    'M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 0 1-2.244 2.077H8.084a2.25 2.25 0 0 1-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 0 0-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 0 1 3.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 0 0-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 0 0-7.5 0',
  up: 'M4.5 15.75l7.5-7.5 7.5 7.5',
  down: 'M19.5 8.25l-7.5 7.5-7.5-7.5',
  wand: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09z',
  x: 'M6 18 18 6M6 6l12 12',
} as const

export function Icon({
  name,
  className = 'h-5 w-5',
}: {
  name: keyof typeof ICON_PATHS
  className?: string
}) {
  return (
    <svg
      className={className}
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      <path strokeLinecap="round" strokeLinejoin="round" d={ICON_PATHS[name]} />
    </svg>
  )
}

/** A live "used / cap" counter that turns red once the value exceeds the cap. */
export function Counter({ value, cap }: { value: number; cap: number }) {
  const over = value > cap
  return (
    <span
      className={`text-xs tabular-nums ${over ? 'font-medium text-red-400' : 'text-dim'}`}
      aria-live="polite"
    >
      {value.toLocaleString()} / {cap.toLocaleString()}
    </span>
  )
}

/** A field label row with an optional trailing node (e.g. a counter). */
export function FieldLabel({
  children,
  trailing,
  htmlFor,
}: {
  children: React.ReactNode
  trailing?: React.ReactNode
  htmlFor?: string
}) {
  return (
    <div className="mb-1 flex items-center justify-between gap-2">
      <label htmlFor={htmlFor} className="text-xs font-medium uppercase tracking-wide text-dim">
        {children}
      </label>
      {trailing}
    </div>
  )
}

/** Inline validation message shown under a field. */
export function FieldError({ message }: { message?: string }) {
  if (!message) return null
  return <p className="mt-1 text-xs text-red-400">{message}</p>
}

/** A small square icon-only button (the entry cards' move/remove controls). */
export function IconButton({
  children,
  label,
  onClick,
  disabled,
}: {
  children: React.ReactNode
  label: string
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      disabled={disabled}
      className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-edge text-dim transition-colors hover:border-pink-500 hover:text-pink-300 disabled:cursor-not-allowed disabled:opacity-30 disabled:hover:border-edge disabled:hover:text-dim"
    >
      {children}
    </button>
  )
}

/** The shell of one editable entry (a character card / recap entry): the shared
    card surface plus the header row with the title and up/down/remove controls.
    The editors render their fields as children. */
export function EntryCard({
  title,
  index,
  count,
  removeLabel,
  onMove,
  onRemove,
  children,
}: {
  title: string
  index: number
  count: number
  removeLabel: string
  onMove: (index: number, dir: -1 | 1) => void
  onRemove: (index: number) => void
  children: React.ReactNode
}) {
  return (
    <div className="rounded-2xl border border-edge bg-surface p-5">
      <div className="mb-3 flex items-center justify-between gap-2">
        <span className="text-sm font-semibold text-hi">{title}</span>
        <div className="flex items-center gap-1">
          <IconButton label="Move up" disabled={index === 0} onClick={() => onMove(index, -1)}>
            <Icon name="up" className="h-4 w-4" />
          </IconButton>
          <IconButton
            label="Move down"
            disabled={index === count - 1}
            onClick={() => onMove(index, 1)}
          >
            <Icon name="down" className="h-4 w-4" />
          </IconButton>
          <IconButton label={removeLabel} onClick={() => onRemove(index)}>
            <Icon name="trash" className="h-4 w-4" />
          </IconButton>
        </div>
      </div>
      {children}
    </div>
  )
}
