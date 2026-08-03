/** Sizing the homepage's big catalogue numbers so they never outgrow their box.

    The stats band used to set every total at a fixed display size (3rem from
    `md` up). That held while the catalogue was small and stops holding as it
    grows: at the five-column breakpoint a tile is only ~150px wide inside its
    padding, and "1,257,183" at 3rem is far wider than that. The overflow is
    not clipped so much as painted over by the NEXT tile's opaque background,
    so the number reads as truncated ("1,257,18").

    So the size is fluid against the tile's OWN inline size (a container query,
    which also covers the two layouts where tiles span different column counts)
    and scaled by how many characters the number formats to. The band applies
    one size to the whole row - taken from its longest value - so the row stays
    a row of equals rather than five different sizes. */

/** Average advance per character of the formatted number, in em. Roboto Black
    digits run ~0.6em and a comma far less; measured in the browser against
    "1,257,183" this comes out at 0.580em per character including
    `tracking-tight`. */
export const AVG_ADVANCE_EM = 0.58

/** How much of the tile the number is allowed to fill. Sizing it to exactly
    the box is arithmetically correct and looks wrong - the digits touch the
    border - and leaves nothing for a font that measures a shade wider than
    Roboto (a fallback while the webfont loads, say). */
export const FILL = 0.9

/** Floor and ceiling, in rem. The ceiling is the size the band has always used
    at its widest. The floor is what the number stops shrinking at; it is set
    low enough that even a smallest-phone tile holding an eight-figure total
    still fits, since a floor that clips is the very bug being fixed. In
    practice it never binds - the fluid term is above it at every real width. */
export const MIN_STAT_REM = 1
export const MAX_STAT_REM = 3

/** The band formats every number the same way; the length of THIS string is
    what the size is computed from, so the two can never disagree. */
export function formatStat(n: number): string {
  return n.toLocaleString('en-GB')
}

/** The `font-size` value for a number of `chars` characters, as CSS. Uses
    container-query units, so the element must sit inside a container. */
export function statFontSize(chars: number): string {
  const cqi = (100 * FILL) / (Math.max(1, chars) * AVG_ADVANCE_EM)
  return `clamp(${MIN_STAT_REM}rem, ${cqi.toFixed(2)}cqi, ${MAX_STAT_REM}rem)`
}

/** What `statFontSize` resolves to, in px, for a tile whose content box is
    `tilePx` wide - the same clamp, evaluated in TypeScript so the fit can be
    asserted without a browser. */
export function resolveStatFontSize(chars: number, tilePx: number, rootPx = 16): number {
  const fluid = (tilePx * FILL) / (Math.max(1, chars) * AVG_ADVANCE_EM)
  return Math.min(MAX_STAT_REM * rootPx, Math.max(MIN_STAT_REM * rootPx, fluid))
}
