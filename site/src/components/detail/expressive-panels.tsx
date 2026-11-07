// The community-authored expressive layer, rendered: the characters grid and the
// "story so far" list.
//
// They live HERE rather than inside WorkDetail.tsx because they now have two
// call sites - the work page's tabs and the two crawlable guide pages
// (/works/{slug}/characters, /works/{slug}/recap, see work-guide.tsx). One
// implementation, so a spoiler rule fixed on the work page is fixed on the guide
// page in the same edit; the pure row/label rules they render stay in
// lib/expressive.ts.

import type { Character } from '../../lib/api'
import { roleLabel, revealLabel, type StoryRow as StoryRowData } from '../../lib/expressive'
import { Badge } from '../ui'

/** Character aliases and descriptions stay in a closed native disclosure,
    scoped to the reveal position. Names remain ordinary headings. */
function CharacterCard({ character, unit }: { character: Character; unit?: string }) {
  const role = roleLabel(character.role)
  const hasSpoilers = Boolean(character.description || character.aliases?.length)
  const reveal = (
    <span className="text-xs font-medium text-pink-400/90">{revealLabel(character.reveal, unit)}</span>
  )

  return (
    <article className="overflow-hidden rounded-2xl border border-edge bg-surface p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="font-semibold text-hi">{character.name}</h3>
        </div>
        {role ? <Badge>{role}</Badge> : null}
      </div>
      {hasSpoilers ? (
        <details className="mt-2">
          <summary className="cursor-pointer text-xs font-medium text-pink-400/90">
            {reveal}
          </summary>
          <div data-nosnippet className="mt-3 space-y-2 text-sm leading-relaxed text-body">
            {character.aliases?.length ? <p>Also known as {character.aliases.join(', ')}.</p> : null}
            {character.description ? <p>{character.description}</p> : null}
          </div>
        </details>
      ) : <p className="mt-2">{reveal}</p>}
    </article>
  )
}

/** The cast of a work: community-authored, spoiler-aware character cards, each a
    per-card accordion so descriptions stay hidden until opened. */
export function CharactersPanel({ characters, unit }: { characters: Character[]; unit?: string }) {
  return (
    <>
      <p className="mt-6 max-w-2xl text-sm text-dim">
        Community-written and spoiler-aware - open a character to read who they are, scoped to where
        they first appear.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {characters.map((c) => (
          <CharacterCard key={c.id} character={c} unit={unit} />
        ))}
      </div>
    </>
  )
}

/** One "story so far" row: a collapsible accordion that stays closed (spoiler-safe)
    until the reader opens it. Shared by the position-keyed chaptered recaps and the
    whole-book summary rows ("In short" / "How did it end?"), so all three read as
    one list; the caller supplies the header title, an optional scope/kind badge,
    and the revealed body text. */
function StoryRow({ title, badge, text }: { title: string; badge?: string; text: string }) {
  return (
    <details className="bg-surface">
      <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-body transition-colors hover:bg-raised/40">
        {title}{badge ? <span className="ml-2"><Badge>{badge}</Badge></span> : null}
      </summary>
      <p data-nosnippet className="px-4 pb-4 text-sm leading-relaxed text-body">{text}</p>
    </details>
  )
}

/** "Story so far": the rows built by storyRows (the whole-book "In short" row
    first, the position-ordered chaptered recaps, the whole-book "How did it end?"
    row last) - every row an accordion closed by default so the reader chooses how
    far to reveal. The chaptered rows are bounded to their position; the whole-book
    summary rows are full spoilers, and the intro's spoiler clause appears only
    when such a row is present. A work may carry only the summary (no chaptered
    recaps), in which case just the "In short"/ending rows show. */
export function RecapsPanel({ rows }: { rows: StoryRowData[] }) {
  const hasWholeBook = rows.some((r) => r.wholeBook)
  return (
    <>
      <p className="mt-6 max-w-2xl text-sm text-dim">
        Open a recap only as far as you have listened
        {hasWholeBook ? ' - the whole-book rows are full spoilers.' : '.'}
      </p>
      <div className="mt-5 divide-y divide-edge/60 overflow-hidden rounded-2xl border border-edge">
        {rows.map((row, i) => (
          <StoryRow key={`${row.title}-${i}`} {...row} />
        ))}
      </div>
    </>
  )
}
