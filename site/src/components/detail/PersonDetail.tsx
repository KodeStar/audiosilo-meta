import { getPerson, type Person, type WorkCard as WorkCardData } from '../../lib/api'
import {
  authoredNote,
  narratedWorks,
  narrationNote,
  workCount,
} from '../../lib/person-credits'
import WorkCard from '../cards/WorkCard'
import {
  useQueryParam,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
  ImproveRecord,
} from './detail-common'

function WorkGrid({ works }: { works: WorkCardData[] }) {
  return (
    <div className="grid grid-cols-2 gap-5 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
      {works.map((w) => (
        <WorkCard key={w.id} work={w} />
      ))}
    </div>
  )
}

/** One credit section. `count` is the count of the cards rendered below, already
    written with its unit (see person-credits.workCount); `note` carries the
    truncation line when the API returned a page rather than the whole list, so a
    prolific narrator's page says what it is showing instead of quietly
    under-reporting. */
function Section({
  title,
  count,
  note,
  children,
}: {
  title: string
  count: string
  note?: string
  children: React.ReactNode
}) {
  return (
    <section className="mt-12 first:mt-0">
      <h2 className="mb-5 text-xl font-semibold text-hi">
        {title}
        <span className="ml-2 text-sm font-normal text-dim">{count}</span>
      </h2>
      {note ? <p className="mb-5 text-sm text-dim">{note}</p> : null}
      {children}
    </section>
  )
}

function Loaded({ person }: { person: Person }) {
  usePageTitle(person.name)

  const narrated: WorkCardData[] = narratedWorks(person.narrated)

  const roles: string[] = []
  if ((person.authored?.length ?? 0) > 0) roles.push('Author')
  if (narrated.length > 0) roles.push('Narrator')

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <header className="mb-10">
        <h1 className="text-3xl font-bold tracking-tight text-hi sm:text-4xl">{person.name}</h1>
        {roles.length > 0 ? (
          <div className="mt-3 flex flex-wrap gap-2">
            {roles.map((r) => (
              <span
                key={r}
                className="rounded-full border border-edge bg-surface px-3 py-1 text-xs font-medium text-dim"
              >
                {r}
              </span>
            ))}
          </div>
        ) : null}
      </header>

      {/* One rule for both sections: the heading counts the cards rendered below
          it and NAMES that unit, and the note - shown only when the API returned
          a window - states that window in the API's own unit and names it too.
          The two units genuinely differ for a narrator (several recordings of
          one work are several credits, one card), so leaving either number bare
          made a truncated narrator page look self-contradictory. */}
      {person.authored && person.authored.length > 0 ? (
        <Section
          title="Wrote"
          count={workCount(person.authored.length)}
          note={authoredNote(person.authored.length, person.authored_total)}
        >
          <WorkGrid works={person.authored} />
        </Section>
      ) : null}

      {narrated.length > 0 ? (
        <Section
          title="Narrated"
          count={workCount(narrated.length)}
          note={narrationNote(
            person.narrated?.length ?? 0,
            person.narrated_total,
            narrated.length
          )}
        >
          <WorkGrid works={narrated} />
        </Section>
      ) : null}

      {(person.authored?.length ?? 0) === 0 && narrated.length === 0 ? (
        <p className="rounded-xl border border-edge bg-surface px-6 py-12 text-center text-sm text-dim">
          No works are linked to this person yet.
        </p>
      ) : null}

      <ImproveRecord kind="person" id={person.id} />
    </div>
  )
}

export default function PersonDetail() {
  const id = useQueryParam('id')
  const state = useEntity<Person>(id, getPerson)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="person" />
  return <Loaded person={state.data} />
}
