// The community-authored expressive layer, rendered: the characters grid and the
// "story so far" list.
//
// They live HERE rather than inside WorkDetail.tsx because they now have two
// call sites - the work page's tabs and the two crawlable guide pages
// (/works/{slug}/characters, /works/{slug}/recap, see work-guide.tsx). One
// implementation, so a spoiler rule fixed on the work page is fixed on the guide
// page in the same edit; the pure row/label rules they render stay in
// lib/expressive.ts.

import { useState } from 'react'
import type { Character } from '../../lib/api'
import { roleLabel, revealLabel, type StoryRow as StoryRowData } from '../../lib/expressive'
import { Badge, Chevron } from '../ui'

/** One character card. The description is spoiler-bounded to the reveal position
    but still story detail, so it stays hidden behind a per-card accordion the
    reader opens via the reveal-position row. The name stays a real heading in
    both branches; a card with no description has no disclosure control. */
function CharacterCard({ character }: { character: Character }) {
  const [open, setOpen] = useState(false)
  const role = roleLabel(character.role)
  const hasDescription = Boolean(character.description)
  const descId = `char-desc-${character.id}`
  const reveal = (
    <span className="text-xs font-medium text-pink-400/90">{revealLabel(character.reveal)}</span>
  )

  return (
    <article className="overflow-hidden rounded-2xl border border-edge bg-surface p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="font-semibold text-hi">{character.name}</h3>
          {character.aliases && character.aliases.length > 0 ? (
            <p className="mt-0.5 text-xs text-dim">also {character.aliases.join(', ')}</p>
          ) : null}
        </div>
        {role ? <Badge>{role}</Badge> : null}
      </div>
      {hasDescription ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-controls={descId}
          className="mt-2 flex w-full items-center justify-between gap-2 text-left transition-colors hover:opacity-80"
        >
          {reveal}
          <Chevron open={open} className="shrink-0" />
        </button>
      ) : (
        <p className="mt-2">{reveal}</p>
      )}
      {hasDescription && open ? (
        <p id={descId} className="mt-3 text-sm leading-relaxed text-body">
          {character.description}
        </p>
      ) : null}
    </article>
  )
}

/** The cast of a work: community-authored, spoiler-aware character cards, each a
    per-card accordion so descriptions stay hidden until opened. */
export function CharactersPanel({ characters }: { characters: Character[] }) {
  return (
    <>
      <p className="mt-6 max-w-2xl text-sm text-dim">
        Community-written and spoiler-aware - open a character to read who they are, scoped to where
        they first appear.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {characters.map((c) => (
          <CharacterCard key={c.id} character={c} />
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
  const [open, setOpen] = useState(false)
  return (
    <div className="bg-surface">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-raised/40"
      >
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-body">{title}</span>
          {badge ? <Badge>{badge}</Badge> : null}
        </span>
        <Chevron open={open} className="shrink-0" />
      </button>
      {open ? <p className="px-4 pb-4 text-sm leading-relaxed text-body">{text}</p> : null}
    </div>
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
