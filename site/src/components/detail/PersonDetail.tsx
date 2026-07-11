import { getPerson, type Person, type WorkCard as WorkCardData } from '../../lib/api'
import WorkCard from '../cards/WorkCard'
import {
  useQueryParam,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
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

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <section className="mt-12 first:mt-0">
      <h2 className="mb-5 text-xl font-semibold text-hi">
        {title}
        <span className="ml-2 text-sm font-normal text-dim">{count}</span>
      </h2>
      {children}
    </section>
  )
}

function Loaded({ person }: { person: Person }) {
  usePageTitle(person.name)

  // De-duplicate narrated works by work id (a narrator can appear on several
  // recordings of the same work).
  const narratedWorks: WorkCardData[] = []
  const seen = new Set<string>()
  for (const n of person.narrated ?? []) {
    if (n.work && !seen.has(n.work.id)) {
      seen.add(n.work.id)
      narratedWorks.push(n.work)
    }
  }

  const roles: string[] = []
  if ((person.authored?.length ?? 0) > 0) roles.push('Author')
  if (narratedWorks.length > 0) roles.push('Narrator')

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

      {person.authored && person.authored.length > 0 ? (
        <Section title="Wrote" count={person.authored.length}>
          <WorkGrid works={person.authored} />
        </Section>
      ) : null}

      {narratedWorks.length > 0 ? (
        <Section title="Narrated" count={narratedWorks.length}>
          <WorkGrid works={narratedWorks} />
        </Section>
      ) : null}

      {(person.authored?.length ?? 0) === 0 && narratedWorks.length === 0 ? (
        <p className="rounded-xl border border-edge bg-surface px-6 py-12 text-center text-sm text-dim">
          No works are linked to this person yet.
        </p>
      ) : null}
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
