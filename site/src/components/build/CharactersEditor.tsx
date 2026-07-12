import {
  CAPS,
  type CharacterDraft,
  type CharacterCardErrors,
  type CharacterRole,
} from '../../lib/builder'
import {
  BTN_SECONDARY,
  Counter,
  EntryCard,
  FieldError,
  FieldLabel,
  Icon,
  INPUT,
  TEXTAREA,
} from './build-ui'

const ROLES: { value: CharacterRole | ''; label: string }[] = [
  { value: '', label: 'None / unclear' },
  { value: 'protagonist', label: 'Protagonist' },
  { value: 'antagonist', label: 'Antagonist' },
  { value: 'supporting', label: 'Supporting' },
  { value: 'minor', label: 'Minor' },
]

interface Props {
  drafts: CharacterDraft[]
  errors: CharacterCardErrors[]
  onName: (index: number, name: string) => void
  onPatch: (index: number, patch: Partial<CharacterDraft>) => void
  onAdd: () => void
  onRemove: (index: number) => void
  onMove: (index: number, dir: -1 | 1) => void
}

export default function CharactersEditor({
  drafts,
  errors,
  onName,
  onPatch,
  onAdd,
  onRemove,
  onMove,
}: Props) {
  return (
    <div className="space-y-4">
      {drafts.map((d, i) => {
        const e = errors[i] ?? {}
        return (
          <EntryCard
            key={i}
            title={`Character ${i + 1}`}
            index={i}
            count={drafts.length}
            removeLabel="Remove character"
            onMove={onMove}
            onRemove={onRemove}
          >
            {d.seeded ? (
              <p className="mb-3 rounded-lg border border-pink-500/30 bg-pink-600/5 px-3 py-2 text-xs leading-relaxed text-pink-200">
                Seeded from the previous book. Re-check the reveal chapter for this book and write a
                fresh description for where this character stands here.
              </p>
            ) : null}

            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <FieldLabel htmlFor={`name-${i}`}>Name</FieldLabel>
                <input
                  id={`name-${i}`}
                  className={INPUT}
                  value={d.name}
                  onChange={(ev) => onName(i, ev.target.value)}
                  placeholder="Orion Lake"
                />
                <FieldError message={e.name} />
              </div>
              <div>
                <FieldLabel htmlFor={`id-${i}`}>Id (slug)</FieldLabel>
                <input
                  id={`id-${i}`}
                  className={INPUT}
                  value={d.id}
                  onChange={(ev) => onPatch(i, { id: ev.target.value })}
                  placeholder="orion-lake"
                  spellCheck={false}
                />
                <FieldError message={e.id} />
              </div>
              <div>
                <FieldLabel htmlFor={`aliases-${i}`}>Aliases (comma-separated)</FieldLabel>
                <input
                  id={`aliases-${i}`}
                  className={INPUT}
                  value={d.aliasesText}
                  onChange={(ev) => onPatch(i, { aliasesText: ev.target.value })}
                  placeholder="Galadriel Higgins, Galadriel"
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <FieldLabel htmlFor={`role-${i}`}>Role</FieldLabel>
                  <select
                    id={`role-${i}`}
                    className={INPUT}
                    value={d.role}
                    onChange={(ev) =>
                      onPatch(i, { role: ev.target.value as CharacterRole | '' })
                    }
                  >
                    {ROLES.map((r) => (
                      <option key={r.value} value={r.value}>
                        {r.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div>
                  <FieldLabel htmlFor={`reveal-${i}`}>Reveal chapter</FieldLabel>
                  <input
                    id={`reveal-${i}`}
                    className={INPUT}
                    inputMode="numeric"
                    value={d.reveal}
                    onChange={(ev) => onPatch(i, { reveal: ev.target.value })}
                    placeholder="1"
                  />
                  <FieldError message={e.reveal} />
                </div>
              </div>
            </div>

            <div className="mt-4">
              <FieldLabel
                htmlFor={`desc-${i}`}
                trailing={<Counter value={d.description.length} cap={CAPS.description} />}
              >
                Description
              </FieldLabel>
              <textarea
                id={`desc-${i}`}
                className={TEXTAREA}
                value={d.description}
                onChange={(ev) => onPatch(i, { description: ev.target.value })}
                placeholder="Your own words, for a reader who has just reached the reveal chapter - no later twists."
              />
              <FieldError message={e.description} />
            </div>

            <div className="mt-4 max-w-xs">
              <FieldLabel htmlFor={`wd-${i}`}>Wikidata QID (optional)</FieldLabel>
              <input
                id={`wd-${i}`}
                className={INPUT}
                value={d.wikidata}
                onChange={(ev) => onPatch(i, { wikidata: ev.target.value })}
                placeholder="Q12345"
                spellCheck={false}
              />
              <FieldError message={e.wikidata} />
              <p className="mt-1 text-xs text-dim">
                A shared QID links the same character across a series.
              </p>
            </div>
          </EntryCard>
        )
      })}

      <button type="button" onClick={onAdd} className={`${BTN_SECONDARY} px-4 py-2 text-sm`}>
        <Icon name="plus" className="h-4 w-4" />
        Add a character
      </button>
    </div>
  )
}
