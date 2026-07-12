// Editor-specific presentational primitives for the /build editors. The
// cross-island primitives (button/pill classes, Icon) live in ../ui.tsx - one
// source of truth; this module holds only what the sidecar editors add on top.

import { Icon } from '../ui'

// Shared form-control classes.
export const INPUT =
  'w-full rounded-lg border border-edge bg-raised px-3 py-2 text-sm text-hi placeholder:text-dim/50 focus:border-pink-500 focus:outline-none'
export const TEXTAREA = `${INPUT} min-h-[6rem] leading-relaxed`

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
