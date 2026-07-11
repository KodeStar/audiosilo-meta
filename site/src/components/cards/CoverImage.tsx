import { useState } from 'react'

interface Props {
  src?: string | null
  alt: string
  /** Title used inside the fallback tile when no cover is available. */
  title?: string
  className?: string
  /** Eager-load the first cover (detail hero); grid covers stay lazy. */
  eager?: boolean
}

/** A book cover with a graceful fallback: a navy tile with a book glyph and the
    title, shown when there is no cover URL or the image fails to load. Every
    cover keeps a 2:3 aspect box so grids never reflow as images arrive. */
export default function CoverImage({
  src,
  alt,
  title,
  className = '',
  eager = false,
}: Props) {
  const [failed, setFailed] = useState(false)
  const showFallback = !src || failed

  return (
    <div
      className={`relative aspect-[2/3] w-full overflow-hidden rounded-lg border border-edge bg-raised ${className}`}
    >
      {showFallback ? (
        <div className="flex h-full w-full flex-col items-center justify-center gap-2 p-3 text-center">
          <svg
            className="h-8 w-8 text-edge"
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
              d="M12 6.042A8.967 8.967 0 0 0 6 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 0 1 6 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 0 1 6-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0 0 18 18a8.967 8.967 0 0 0-6 2.292m0-14.25v14.25"
            />
          </svg>
          {title ? (
            <span className="line-clamp-2 text-xs leading-snug text-dim">{title}</span>
          ) : null}
        </div>
      ) : (
        <img
          src={src ?? undefined}
          alt={alt}
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          className="h-full w-full object-cover"
          onError={() => setFailed(true)}
        />
      )}
    </div>
  )
}
