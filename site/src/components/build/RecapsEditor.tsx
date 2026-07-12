import {
  CAPS,
  type RecapsDraft,
  type RecapEntryDraft,
  type RecapsValidation,
  type RecapScope,
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

const SCOPES: { value: RecapScope | ''; label: string }[] = [
  { value: '', label: 'None' },
  { value: 'book', label: 'This book only' },
  { value: 'series', label: 'Series (covers earlier books)' },
]

interface Props {
  draft: RecapsDraft
  errors: RecapsValidation
  onEntry: (index: number, patch: Partial<RecapEntryDraft>) => void
  onAdd: () => void
  onRemove: (index: number) => void
  onMove: (index: number, dir: -1 | 1) => void
  onSummary: (field: 'inShort' | 'ending', value: string) => void
}

export default function RecapsEditor({
  draft,
  errors,
  onEntry,
  onAdd,
  onRemove,
  onMove,
  onSummary,
}: Props) {
  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-sm font-semibold text-hi">Chaptered recaps</h3>
        {draft.entries.map((e, i) => {
          const err = errors.entries[i] ?? {}
          return (
            <EntryCard
              key={i}
              title={`Recap ${i + 1}`}
              index={i}
              count={draft.entries.length}
              removeLabel="Remove recap"
              onMove={onMove}
              onRemove={onRemove}
            >
              <div className="grid gap-4 sm:grid-cols-2">
                <div>
                  <FieldLabel htmlFor={`through-${i}`}>Through chapter</FieldLabel>
                  <input
                    id={`through-${i}`}
                    className={INPUT}
                    inputMode="numeric"
                    value={e.through}
                    onChange={(ev) => onEntry(i, { through: ev.target.value })}
                    placeholder="3"
                  />
                  <FieldError message={err.through} />
                </div>
                <div>
                  <FieldLabel htmlFor={`scope-${i}`}>Scope</FieldLabel>
                  <select
                    id={`scope-${i}`}
                    className={INPUT}
                    value={e.scope}
                    onChange={(ev) => onEntry(i, { scope: ev.target.value as RecapScope | '' })}
                  >
                    {SCOPES.map((s) => (
                      <option key={s.value} value={s.value}>
                        {s.label}
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              <div className="mt-4">
                <FieldLabel
                  htmlFor={`text-${i}`}
                  trailing={<Counter value={e.text.length} cap={CAPS.recapText} />}
                >
                  Recap text
                </FieldLabel>
                <textarea
                  id={`text-${i}`}
                  className={TEXTAREA}
                  value={e.text}
                  onChange={(ev) => onEntry(i, { text: ev.target.value })}
                  placeholder="Own words, safe to show once the listener has finished this chapter and nothing after."
                />
                <FieldError message={err.text} />
              </div>
            </EntryCard>
          )
        })}

        <button type="button" onClick={onAdd} className={`${BTN_SECONDARY} px-4 py-2 text-sm`}>
          <Icon name="plus" className="h-4 w-4" />
          Add a recap
        </button>
      </div>

      <div className="space-y-4">
        <div>
          <h3 className="text-sm font-semibold text-hi">Whole-book summaries (optional)</h3>
          <p className="mt-1 text-sm leading-relaxed text-dim">
            For a reader who has finished the book and wants a fast refresher before the next one.
            Both are full spoilers and state the real ending plainly - never a tease.
          </p>
        </div>

        <div className="rounded-2xl border border-edge bg-surface p-5">
          <FieldLabel
            htmlFor="in-short"
            trailing={<Counter value={draft.inShort.length} cap={CAPS.inShort} />}
          >
            In short
          </FieldLabel>
          <textarea
            id="in-short"
            className={TEXTAREA}
            value={draft.inShort}
            onChange={(ev) => onSummary('inShort', ev.target.value)}
            placeholder="The whole arc in one paragraph, ending included."
          />
          <FieldError message={errors.inShort} />
        </div>

        <div className="rounded-2xl border border-edge bg-surface p-5">
          <FieldLabel
            htmlFor="ending"
            trailing={<Counter value={draft.ending.length} cap={CAPS.ending} />}
          >
            Ending
          </FieldLabel>
          <textarea
            id="ending"
            className={TEXTAREA}
            value={draft.ending}
            onChange={(ev) => onSummary('ending', ev.target.value)}
            placeholder="How the book closes: where every major player stands and which threads stay open."
          />
          <FieldError message={errors.ending} />
        </div>
      </div>
    </div>
  )
}
