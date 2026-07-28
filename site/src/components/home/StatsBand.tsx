import { useEffect, useRef, useState } from 'react'
import { getStats, getCoverage, type Stats, type CoverageResponse } from '../../lib/api'

/** Which arrangement of the band to render. Three different answers to "make
    characters and recaps prominent", not three skins:
    - `tiers`   the catalogue numbers stay a quiet row; the expressive layer
                sits above them as a pair of wide cards with copy and a link.
    - `feature` the expressive pair leads at full size; the catalogue collapses
                to a single inline strip of numbers, no boxes at all.
    - `grid`    one grid, in which the two expressive tiles span wider, carry
                the accent and a link, and the rest are unchanged. */
export type StatsLayout = 'tiers' | 'feature' | 'grid'

interface Props {
  layout?: StatsLayout
}

interface Tile {
  label: string
  value: number
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

function Num({ value, run, className }: { value: number; run: boolean; className?: string }) {
  const n = useCountUp(value, run)
  return <span className={className}>{n.toLocaleString('en-GB')}</span>
}

function StatTile({ tile, run }: { tile: Tile; run: boolean }) {
  return (
    <div className="rounded-xl border border-edge bg-surface px-4 py-6 text-center">
      <Num
        value={tile.value}
        run={run}
        className="block text-3xl font-black tracking-tight text-hi sm:text-4xl md:text-5xl"
      />
      <div className="mt-1 text-xs uppercase tracking-wider text-dim sm:text-sm">{tile.label}</div>
    </div>
  )
}

/** The expressive layer - the community-written characters and recaps. It is
    the part of this database nothing else has, so every layout gives it the
    accent, a sentence of explanation, and a link to the works that carry it. */
interface Feature {
  key: 'characters' | 'recaps'
  /** Background art, treated like the hero so the page reads as one material. */
  art: string
  value: number
  label: string
  blurb: string
  href: string
}

function features(cov: CoverageResponse): Feature[] {
  return [
    {
      key: 'characters',
      art: '/img/card/characters.webp',
      value: cov.totals.with_characters ?? 0,
      label: 'Works with characters',
      blurb: 'Who is who, written by listeners and revealed only as far as you have listened.',
      href: '/contribute?filter=has_characters',
    },
    {
      key: 'recaps',
      art: '/img/card/recaps.webp',
      value: cov.totals.with_recaps ?? 0,
      label: 'Works with recaps',
      blurb: 'The story so far, chapter by chapter, with nothing ahead of you given away.',
      href: '/contribute?filter=has_recaps',
    },
  ]
}

function FeatureCard({ f, run, large }: { f: Feature; run: boolean; large?: boolean }) {
  return (
    <a
      href={f.href}
      className="group relative block overflow-hidden rounded-2xl border border-pink-600/25 bg-surface p-5 transition-colors hover:border-pink-500/60 sm:p-6"
    >
      {/* Photograph + accent wash, in paint order beneath the content. Same
          recipe as the hero, so these cards are made of the same material as
          the rest of the page rather than being a coloured box. */}
      <span
        aria-hidden="true"
        className="stat-art pointer-events-none absolute inset-0"
        style={{ backgroundImage: `url('${f.art}')` }}
      />
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 bg-gradient-to-br from-pink-600/20 via-transparent to-deep/60"
      />
      <div className="relative flex items-start gap-4">
        <div className="min-w-0">
          <Num
            value={f.value}
            run={run}
            className={`block font-black tracking-tight text-hi ${
              large ? 'text-4xl sm:text-5xl' : 'text-3xl sm:text-4xl'
            }`}
          />
          <div className="mt-0.5 text-xs uppercase tracking-wider text-pink-400 sm:text-sm">
            {f.label}
          </div>
          <p className="mt-2 text-sm leading-relaxed text-body">{f.blurb}</p>
          <span className="mt-3 inline-flex items-center gap-1.5 text-sm font-medium text-pink-400 transition-colors group-hover:text-pink-300">
            Browse them
            <span aria-hidden="true" className="transition-transform group-hover:translate-x-0.5">
              &rarr;
            </span>
          </span>
        </div>
      </div>
    </a>
  )
}

export default function StatsBand({ layout = 'tiers' }: Props) {
  const [stats, setStats] = useState<Stats | null>(null)
  const [cov, setCov] = useState<CoverageResponse | null>(null)
  const [error, setError] = useState(false)
  const [visible, setVisible] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    getStats(ctrl.signal)
      .then(setStats)
      .catch((err) => {
        if ((err as Error).name !== 'AbortError') setError(true)
      })
    // Coverage is a SEPARATE endpoint and a softer failure: if it cannot be
    // read we still show the catalogue totals rather than blanking the band.
    getCoverage(ctrl.signal)
      .then(setCov)
      .catch(() => undefined)
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
  const feats = cov ? features(cov) : []

  // No coverage data - fall back to the plain five-tile band.
  if (feats.length === 0) {
    return (
      <div ref={ref} className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {tiles.map((t) => (
          <StatTile key={t.label} tile={t} run={visible} />
        ))}
      </div>
    )
  }

  if (layout === 'feature') {
    return (
      <div ref={ref} className="space-y-6">
        <div className="grid gap-4 md:grid-cols-2">
          {feats.map((f) => (
            <FeatureCard key={f.key} f={f} run={visible} large />
          ))}
        </div>
        {/* The catalogue's scale, stated once and quietly - the pair above is
            what the band is actually arguing for. */}
        <dl className="flex flex-wrap items-baseline justify-center gap-x-8 gap-y-3 rounded-xl border border-edge bg-surface px-6 py-4">
          {tiles.map((t) => (
            <div key={t.label} className="flex items-baseline gap-2">
              <Num
                value={t.value}
                run={visible}
                className="text-lg font-bold tabular-nums text-hi sm:text-xl"
              />
              <dt className="text-xs uppercase tracking-wider text-dim">{t.label}</dt>
            </div>
          ))}
        </dl>
      </div>
    )
  }

  if (layout === 'grid') {
    // 6 columns: each feature spans 3, then the five tiles run 2+2+2 / 3+3.
    return (
      <div ref={ref} className="grid gap-4 sm:grid-cols-2 lg:grid-cols-6">
        {feats.map((f) => (
          <div key={f.key} className="lg:col-span-3">
            <FeatureCard f={f} run={visible} />
          </div>
        ))}
        {tiles.map((t, i) => (
          <div key={t.label} className={i < 3 ? 'lg:col-span-2' : 'lg:col-span-3'}>
            <StatTile tile={t} run={visible} />
          </div>
        ))}
      </div>
    )
  }

  // 'tiers' (default)
  return (
    <div ref={ref} className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        {feats.map((f) => (
          <FeatureCard key={f.key} f={f} run={visible} />
        ))}
      </div>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {tiles.map((t) => (
          <StatTile key={t.label} tile={t} run={visible} />
        ))}
      </div>
    </div>
  )
}
