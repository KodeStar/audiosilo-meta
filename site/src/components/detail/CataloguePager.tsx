import { pageWindow } from '../../lib/catalogue'
import { TEXT_LINK } from '../ui'

export default function CataloguePager({ offset, limit, total, pageHref, label }: {
  offset: number
  limit: number
  total: number
  pageHref: (page: number) => string
  label: string
}) {
  const { page, previous, next } = pageWindow(offset, limit, total)
  if (previous === null && next === null) return null
  return (
    <nav aria-label={label} className="mt-6 flex flex-wrap items-center justify-between gap-4 text-sm">
      {previous ? <a rel="prev" href={pageHref(previous)} className={TEXT_LINK}>Previous page</a> : <span />}
      <span className="text-dim">Page {page} of {Math.max(1, Math.ceil(total / limit))}</span>
      {next ? <a rel="next" href={pageHref(next)} className={TEXT_LINK}>Next page</a> : <span />}
    </nav>
  )
}
