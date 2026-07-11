import type { PersonRef } from '../../lib/api'
import { href } from '../../lib/api'

interface Props {
  people: PersonRef[]
  className?: string
  /** Prefix label read only by screen readers (e.g. "Narrated by"). */
  srLabel?: string
}

/** A comma-separated list of linked people (authors or narrators). */
export default function PersonLinks({ people, className = '', srLabel }: Props) {
  if (!people || people.length === 0) return null
  return (
    <span className={className}>
      {srLabel ? <span className="sr-only">{srLabel} </span> : null}
      {people.map((p, i) => (
        <span key={p.id}>
          <a
            href={href.person(p.id)}
            className="text-body underline-offset-2 transition-colors hover:text-pink-400 hover:underline"
          >
            {p.name}
          </a>
          {i < people.length - 1 ? <span className="text-dim">, </span> : null}
        </span>
      ))}
    </span>
  )
}
