import type { SeriesRef } from '../../lib/api'
import { href } from '../../lib/api'

interface Props {
  series: SeriesRef
  className?: string
}

/** A compact pill linking to a series, with its position when known ("#2.5"). */
export default function SeriesBadge({ series, className = '' }: Props) {
  return (
    <a
      href={href.series(series.id)}
      className={`inline-flex max-w-full items-center gap-1 rounded-full border border-edge bg-raised px-2.5 py-0.5 text-xs text-dim transition-colors hover:border-pink-500/50 hover:text-pink-300 ${className}`}
    >
      <span className="truncate">{series.name}</span>
      {series.position ? (
        <span className="shrink-0 font-semibold text-pink-400">#{series.position}</span>
      ) : null}
    </a>
  )
}
