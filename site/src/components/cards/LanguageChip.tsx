import { languageName, needsBadge, primarySubtag } from '../../lib/languages'

/** The small language chip a work card or a search row wears when its language
    is outside the reader's own (lib/languages.ts needsBadge - the active filter,
    else the browser's languages). It shows the bare subtag ("DE") for the eye and
    the full name to a screen reader and on hover, and renders nothing at all
    when no chip is due, so callers pass it unconditionally. */
export default function LanguageChip({
  language,
  context,
  className = '',
}: {
  language?: string | null
  context: readonly string[]
  className?: string
}) {
  if (!needsBadge(language, context)) return null
  const code = primarySubtag(language)
  const name = languageName(code)
  return (
    <span
      title={name}
      className={`inline-flex shrink-0 items-center rounded border border-edge bg-raised px-1.5 py-px text-[0.6rem] font-semibold uppercase leading-4 tracking-wider text-dim ${className}`}
    >
      <span aria-hidden="true">{code}</span>
      <span className="sr-only">{`In ${name}`}</span>
    </span>
  )
}
