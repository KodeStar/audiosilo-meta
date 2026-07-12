// Shared React-island primitives: the button/pill classes replicated from
// Button.astro and the inline outline Icon (heroicons paths), since Astro
// components cannot render inside a React island. ONE source of truth - the
// import, build and detail islands all pull from here; do not re-copy these
// into an island.

// Button classes replicated from Button.astro.
const BTN_BASE =
  'inline-flex items-center justify-center gap-2 rounded-lg px-6 py-3 font-medium transition-all duration-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:translate-y-0'
export const BTN_PRIMARY = `${BTN_BASE} bg-pink-600 text-white shadow-lg shadow-pink-600/20 hover:-translate-y-0.5 hover:bg-pink-500 hover:shadow-pink-500/30`
export const BTN_SECONDARY = `${BTN_BASE} border border-edge text-hi hover:-translate-y-0.5 hover:border-pink-500`

// The compact pill link used for inline CTAs (coverage rows, work-page CTAs).
// Padding is per call site, so row pills stay tighter than standalone CTAs.
export const PILL_LINK =
  'inline-flex items-center rounded-lg border border-edge text-sm font-medium text-hi transition-colors hover:border-pink-500 hover:text-pink-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500'

const ICON_PATHS = {
  database:
    'M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125',
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
