// How a catalogued release date reads, and when it is still in the future.
//
// The catalogue stores a release date at whatever precision its source stated:
// `YYYY`, `YYYY-MM` or `YYYY-MM-DD` (schema/recording.schema.json). The site
// used to render the YEAR alone everywhere such a value appeared, which is
// fine for a book published once and useless for the shape this page set now
// has to serve: a web serial ships several volumes a year, and a preorder is
// distinguished from a released book by a DAY.
//
// Both functions are string-only on purpose. Date parsing would drag the
// reader's timezone into a fact that has none - `2026-01-01` read as a Date is
// the last day of 2025 west of Greenwich - and would turn an unparseable value
// into "Invalid Date" rather than leaving it alone.
//
// HAND-MIRRORED TWIN of internal/serve/watchfeed.go (`releaseIsFuture`,
// `formatReleaseDate`), which renders the same catalogue values into the watch
// feed a reader subscribes to from this page. A reader comparing the two must
// not be told two different things about one book, so the cases pinned in
// dates.test.ts here and in TestReleaseDatePrecisionMirrorsTheSite there are
// the SAME cases. Change one side, change both.

/** The precision-tolerant shape of every value here: a year, optionally a
    month, optionally a day. Anything else is passed through untouched. */
const RELEASE_DATE = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$/

const MONTHS = [
  'Jan',
  'Feb',
  'Mar',
  'Apr',
  'May',
  'Jun',
  'Jul',
  'Aug',
  'Sep',
  'Oct',
  'Nov',
  'Dec',
]

/**
 * A release date as a person reads it, at the precision the data states:
 * `2026-10-20` -> "20 Oct 2026", `2026-10` -> "Oct 2026", `2026` -> "2026".
 *
 * A value that is not one of those three shapes is returned as stated - the
 * catalogue holds a fact and the page's job is to show it, not to hide what it
 * could not parse. An out-of-range month degrades to the year for the same
 * reason: the year is the part that was still legible.
 */
export function formatReleaseDate(value?: string | null): string | null {
  if (!value) return null
  const raw = value.trim()
  if (!raw) return null
  const m = RELEASE_DATE.exec(raw)
  if (!m) return raw
  const [, year, month, day] = m
  const name = month ? MONTHS[Number(month) - 1] : undefined
  if (!name) return year
  if (!day) return `${name} ${year}`
  return `${Number(day)} ${name} ${year}`
}

/**
 * Is this release date still ahead of `today` (`YYYY-MM-DD`)? That is what
 * makes a catalogued entry a PREORDER rather than something the reader could
 * already be listening to.
 *
 * The comparison is made at the value's OWN precision - a bare `2026` is
 * compared against this year, `2026-10` against this month - because a value
 * that states no day makes no claim about one. Widening it (treating `2026` as
 * `2026-01-01`) would call every book published earlier this year a preorder;
 * narrowing it (`2026-12-31`) would hide a whole year's worth. Strictly after,
 * so a book released today is available.
 *
 * An unparseable or absent value is never a preorder: the reader is shown the
 * ordinary "available" treatment rather than a claim the data does not make.
 */
export function isFutureRelease(value: string | null | undefined, today: string): boolean {
  if (!value) return false
  const raw = value.trim()
  if (!RELEASE_DATE.test(raw)) return false
  return raw > today.slice(0, raw.length)
}
