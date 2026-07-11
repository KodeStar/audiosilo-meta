import { useEffect, useRef, useState } from 'react'
import { getStats, type Stats } from '../../lib/api'

interface Tile {
  label: string
  value: number
  /** Rendered suffix (e.g. "+" is not used; hours get no suffix). */
  format?: (n: number) => string
}

function useCountUp(target: number, run: boolean, durationMs = 1100) {
  const [value, setValue] = useState(0)
  useEffect(() => {
    if (!run) return
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduce) {
      setValue(target)
      return
    }
    let raf = 0
    const start = performance.now()
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / durationMs)
      // easeOutCubic
      const eased = 1 - Math.pow(1 - t, 3)
      setValue(Math.round(eased * target))
      if (t < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [target, run, durationMs])
  return value
}

function StatTile({ tile, run }: { tile: Tile; run: boolean }) {
  const n = useCountUp(tile.value, run)
  const display = tile.format ? tile.format(n) : n.toLocaleString('en-GB')
  return (
    <div className="rounded-xl border border-edge bg-surface px-4 py-6 text-center">
      <div className="text-3xl font-black tracking-tight text-hi sm:text-4xl md:text-5xl">
        {display}
      </div>
      <div className="mt-1 text-xs uppercase tracking-wider text-dim sm:text-sm">
        {tile.label}
      </div>
    </div>
  )
}

export default function StatsBand() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [error, setError] = useState(false)
  const [visible, setVisible] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    getStats(ctrl.signal)
      .then((s) => setStats(s))
      .catch((err) => {
        if ((err as Error).name !== 'AbortError') setError(true)
      })
    return () => ctrl.abort()
  }, [])

  // Trigger the count-up when the band scrolls into view (once).
  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!('IntersectionObserver' in window)) {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((e) => {
          if (e.isIntersecting) {
            setVisible(true)
            io.disconnect()
          }
        })
      },
      { threshold: 0.25 }
    )
    io.observe(el)
    // Safety net: never leave the numbers stuck at 0 if the observer somehow
    // never fires (odd viewport/scroll situations) - reveal after a moment.
    const fallback = window.setTimeout(() => setVisible(true), 1500)
    return () => {
      io.disconnect()
      clearTimeout(fallback)
    }
  }, [])

  if (error) {
    return (
      <div
        ref={ref}
        className="rounded-xl border border-edge bg-surface px-6 py-8 text-center text-sm text-dim"
      >
        Live database totals are unavailable right now.
      </div>
    )
  }

  if (!stats) {
    // Skeleton placeholders keep the band's height stable while loading.
    return (
      <div ref={ref} className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {Array.from({ length: 5 }).map((_, i) => (
          <div
            key={i}
            className="h-[7.5rem] animate-pulse rounded-xl border border-edge bg-surface"
          />
        ))}
      </div>
    )
  }

  const hours = Math.round(stats.total_runtime_min / 60)
  const tiles: Tile[] = [
    { label: 'Works', value: stats.works },
    { label: 'Recordings', value: stats.recordings },
    { label: 'Narrators & authors', value: stats.people },
    { label: 'Hours catalogued', value: hours },
    { label: 'Chapters', value: stats.total_chapters },
  ]

  return (
    <div ref={ref} className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
      {tiles.map((t) => (
        <StatTile key={t.label} tile={t} run={visible} />
      ))}
    </div>
  )
}
