import type { WorkCard as WorkCardData } from '../../lib/api'
import { href } from '../../lib/api'
import CoverImage from './CoverImage'
import PersonLinks from './PersonLinks'
import SeriesBadge from './SeriesBadge'

interface Props {
  work: WorkCardData
}

/** A cover-led card for a work: cover, title, authors, optional series badge.
    The whole cover + title links to the work; authors/series are their own
    links. Used by search, latest additions, and the person/series grids. */
export default function WorkCard({ work }: Props) {
  return (
    <article className="group flex flex-col gap-2">
      <a
        href={href.work(work.id)}
        className="block rounded-lg outline-offset-4 transition-transform duration-200 hover:-translate-y-0.5"
        aria-label={work.title}
      >
        <CoverImage
          src={work.cover_url}
          alt={`Cover of ${work.title}`}
          title={work.title}
          className="transition-colors group-hover:border-pink-500/40"
        />
      </a>
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-medium leading-snug text-hi">
          <a
            href={href.work(work.id)}
            className="line-clamp-2 transition-colors hover:text-pink-300"
          >
            {work.title}
          </a>
        </h3>
        {work.authors && work.authors.length > 0 ? (
          <p className="line-clamp-1 text-xs text-dim">
            <PersonLinks people={work.authors} srLabel="By" />
          </p>
        ) : null}
        {work.series ? (
          <div className="mt-0.5">
            <SeriesBadge series={work.series} />
          </div>
        ) : null}
      </div>
    </article>
  )
}
