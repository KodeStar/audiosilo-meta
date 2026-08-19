// The two community-guide pages of a work - the story-so-far recap and the
// character guide - as ONE island body.
//
// metaserve serves /works/{slug}/recap and /works/{slug}/characters as real
// crawlable pages (a composed head plus a server-rendered fact sheet) so the
// community layer can rank for "<title> recap" and "<title> characters"; this
// island is what a reader with JS then gets in its place, rendering the same
// sidecar through the SAME panels the work page uses (expressive-panels.tsx),
// never a second copy of them.
//
// Everything that differs between the two pages is data (guideSpec below), so
// both shells hydrate THIS component directly - recap.astro and characters.astro
// pass the guide as a prop, which Astro serializes into the island exactly as it
// does for any other client directive.

import type { ReactNode } from 'react'
import { getWork, href, type Work } from '../../lib/api'
import { CC_BY_SA_URL, hasGuide, storyRowsOf, type StoryRow } from '../../lib/expressive'
import type { WorkGuide } from '../../lib/entity-url'
import PersonLinks from '../cards/PersonLinks'
import { PILL_LINK, TEXT_LINK } from '../ui'
import { CharactersPanel, RecapsPanel } from './expressive-panels'
import {
  useWorkGuideSlug,
  useEmbeddedEntity,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
  ImproveRecord,
} from './detail-common'

/** Everything that differs between the recap page and the characters page: the
    sibling page to cross-link, the builder dimension the contribute CTA opens,
    and the empty-state copy for a work that does not carry the member (a stale
    link, or the flat shell reached with an `?id=` under `astro dev`).

    The word that follows the work title in the H1 and the document title is not
    in here: it IS the map key, so the guide value is used directly. */
const guideSpec: Record<
  WorkGuide,
  {
    sibling: WorkGuide
    siblingLabel: string
    build: 'characters' | 'recaps'
    emptyHeading: string
    emptyBody: string
    cta: string
  }
> = {
  recap: {
    sibling: 'characters',
    siblingLabel: 'Character guide',
    build: 'recaps',
    emptyHeading: 'No story so far yet',
    emptyBody:
      'Nobody has written a spoiler-aware recap for this book yet. The guided builder walks you through it, chapter checkpoint by chapter checkpoint.',
    cta: 'Add story so far',
  },
  characters: {
    sibling: 'recap',
    siblingLabel: 'Story so far',
    build: 'characters',
    emptyHeading: 'No character guide yet',
    emptyBody:
      'Nobody has written the cast of this book yet. The guided builder walks you through it, one spoiler-scoped character at a time.',
    cta: 'Add characters',
  },
}

/** The sidecar itself, through the work page's own panels. The recap rows are
    passed in rather than rebuilt here: Loaded already computed them to decide
    between this and the empty state, which is WorkDetail's own compute-once
    pattern. */
function GuidePanel({
  work,
  guide,
  rows,
}: {
  work: Work
  guide: WorkGuide
  rows: StoryRow[]
}): ReactNode {
  if (guide === 'characters') return <CharactersPanel characters={work.characters ?? []} />
  return <RecapsPanel rows={rows} />
}

/** What a work with no such member shows instead: the contribute CTA, which is
    the whole point of landing here on an uncovered work. */
function MissingMember({ work, guide }: { work: Work; guide: WorkGuide }) {
  const spec = guideSpec[guide]
  return (
    <section className="mt-8 rounded-2xl border border-edge bg-surface p-6">
      <h2 className="text-lg font-semibold text-hi">{spec.emptyHeading}</h2>
      <p className="mt-2 max-w-2xl text-sm leading-relaxed text-dim">{spec.emptyBody}</p>
      <div className="mt-5 flex flex-wrap gap-3">
        <a className={`${PILL_LINK} px-4 py-2`} href={href.build(work.id, spec.build)}>
          {spec.cta}
        </a>
        <a className={`${PILL_LINK} px-4 py-2`} href={href.work(work.id)}>
          Back to the work
        </a>
      </div>
    </section>
  )
}

function Loaded({ work, guide, hydrated }: { work: Work; guide: WorkGuide; hydrated: boolean }) {
  const spec = guideSpec[guide]
  usePageTitle(`${work.title} ${guide}`, hydrated)
  // The recap row set, built ONCE and handed to every presence question and the
  // panel - WorkDetail's own compute-once pattern. Whichever of the two pages
  // this is, exactly one of the calls below is about recaps, and it reads these
  // rows rather than rebuilding them (the characters arm ignores the argument).
  const rows = storyRowsOf(work)
  const present = hasGuide(work, guide, rows)
  const siblingPresent = hasGuide(work, spec.sibling, rows)

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <header>
        <h1 className="text-3xl font-bold leading-tight tracking-tight text-hi sm:text-4xl">
          <a href={href.work(work.id)} className="transition-colors hover:text-pink-300">
            {work.title}
          </a>{' '}
          <span className="text-dim">{guide}</span>
        </h1>
        {work.authors && work.authors.length > 0 ? (
          <p className="mt-4 text-base">
            <span className="text-dim">By </span>
            <PersonLinks people={work.authors} className="font-medium" />
          </p>
        ) : null}

        <nav aria-label="This work" className="mt-5 flex flex-wrap gap-3">
          <a className={`${PILL_LINK} px-4 py-2`} href={href.work(work.id)}>
            All about {work.title}
          </a>
          {siblingPresent ? (
            <a className={`${PILL_LINK} px-4 py-2`} href={href.workGuide(work.id, spec.sibling)}>
              {spec.siblingLabel}
            </a>
          ) : null}
        </nav>
      </header>

      {present ? (
        <GuidePanel work={work} guide={guide} rows={rows} />
      ) : (
        <MissingMember work={work} guide={guide} />
      )}

      <p className="mt-12 border-t border-edge pt-6 text-sm text-dim">
        Community-written and published under{' '}
        <a href={CC_BY_SA_URL} rel="license noopener" target="_blank" className={TEXT_LINK}>
          CC BY-SA 3.0
        </a>
        {present ? (
          <>
            {' - '}
            <a href={href.build(work.id, spec.build)} className={TEXT_LINK}>
              add to it or improve it
            </a>
            .
          </>
        ) : (
          '.'
        )}
      </p>
      <ImproveRecord kind="work" id={work.id} />
    </div>
  )
}

/** The island body both guide shells hydrate through. A URL that names no work
    (the shell served untouched at /recap or /characters) and a failed fetch both
    land on the shared detail error state, which points back to search and to the
    contribution routes - never a crash. */
export default function WorkGuidePage({ guide }: { guide: WorkGuide }) {
  const id = useWorkGuideSlug(guide)
  const embedded = useEmbeddedEntity<Work>(id)
  const state = useEntity<Work>(id, getWork, embedded)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="work" />
  return <Loaded work={state.data} guide={guide} hydrated={embedded !== null} />
}
