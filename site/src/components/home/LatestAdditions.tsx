import { useEffect, useState } from 'react'
import { getLatestWorks, type WorkCard as WorkCardData } from '../../lib/api'
import WorkCard from '../cards/WorkCard'

export default function LatestAdditions({ limit = 12 }: { limit?: number }) {
  const [works, setWorks] = useState<WorkCardData[] | null>(null)
  const [error, setError] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    getLatestWorks(limit, ctrl.signal)
      .then((res) => setWorks(res.works ?? []))
      .catch((err) => {
        if ((err as Error).name !== 'AbortError') setError(true)
      })
    return () => ctrl.abort()
  }, [limit])

  if (error) {
    return (
      <p className="rounded-xl border border-edge bg-surface px-6 py-10 text-center text-sm text-dim">
        The latest additions are unavailable right now. Please check back shortly.
      </p>
    )
  }

  if (!works) {
    return (
      <div className="grid grid-cols-2 gap-5 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
        {Array.from({ length: limit }).map((_, i) => (
          <div key={i} className="flex flex-col gap-2">
            <div className="aspect-square w-full animate-pulse rounded-lg border border-edge bg-surface" />
            <div className="h-3 w-3/4 animate-pulse rounded bg-surface" />
          </div>
        ))}
      </div>
    )
  }

  if (works.length === 0) {
    return (
      <p className="rounded-xl border border-edge bg-surface px-6 py-10 text-center text-sm text-dim">
        No entries yet - be the first to contribute one.
      </p>
    )
  }

  return (
    <div className="grid grid-cols-2 gap-5 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
      {works.map((w) => (
        <WorkCard key={w.id} work={w} />
      ))}
    </div>
  )
}
