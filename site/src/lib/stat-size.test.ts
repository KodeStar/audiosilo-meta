import { describe, expect, it } from 'vitest'
import {
  AVG_ADVANCE_EM,
  FILL,
  MAX_STAT_REM,
  formatStat,
  resolveStatFontSize,
  statFontSize,
} from './stat-size'

/** What the number actually draws at, in px, for a tile of `tilePx`. */
function drawn(chars: number, tilePx: number): number {
  return resolveStatFontSize(chars, tilePx) * chars * AVG_ADVANCE_EM
}

/** The width, in px, of a stats tile's CONTENT box at a given viewport - the
    space a number actually has. Mirrors the band's own markup: the page
    `container` (1rem padding, the v2-style max-widths in global.css), a
    `gap-4` grid of 2 / 3 / 5 columns, and the tile's own `px-4`. */
function tileWidth(viewport: number): number {
  const max = [
    [1536, 1536],
    [1280, 1280],
    [1024, 1024],
    [768, 768],
    [640, 640],
  ].find(([bp]) => viewport >= bp)
  const container = (max ? max[1] : viewport) - 32
  const cols = viewport >= 1024 ? 5 : viewport >= 640 ? 3 : 2
  return (container - (cols - 1) * 16) / cols - 32
}

/** Every viewport the band is laid out for, including the two ends and the
    1024px five-column breakpoint where the tile is at its narrowest. */
const VIEWPORTS = [320, 390, 640, 768, 1024, 1280, 1536, 1920]

describe('statFontSize', () => {
  it('keeps the longest catalogue number inside its tile at every width', () => {
    // "1,257,183" - nine characters, the case that was painted over by the
    // next tile's background and read as "1,257,18".
    const chars = formatStat(1_257_183).length
    expect(chars).toBe(9)
    for (const vw of VIEWPORTS) {
      const w = tileWidth(vw)
      // Inside the box AND off the border - `FILL` is the margin it keeps.
      expect(drawn(chars, w), `${vw}px viewport`).toBeLessThanOrEqual(w * FILL)
    }
  })

  it('still fits once the catalogue reaches eight figures', () => {
    const chars = formatStat(12_345_678).length
    for (const vw of VIEWPORTS) {
      const w = tileWidth(vw)
      expect(drawn(chars, w), `${vw}px viewport`).toBeLessThanOrEqual(w)
    }
  })

  it('leaves a short number at the size the band has always used', () => {
    // Four figures in a wide tile: the fluid value is far past the ceiling, so
    // the ceiling is what shows - this is a fix for overflow, not a shrink.
    expect(resolveStatFontSize(formatStat(9_500).length, tileWidth(1536))).toBe(MAX_STAT_REM * 16)
  })

  it('emits a clamp whose ends are the floor and the ceiling', () => {
    expect(statFontSize(9)).toBe('clamp(1rem, 17.24cqi, 3rem)')
    // Longer number, smaller fluid term - the ends never move.
    expect(statFontSize(11)).toBe('clamp(1rem, 14.11cqi, 3rem)')
  })
})
