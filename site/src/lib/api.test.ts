import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { SERIES_ORDERINGS } from './api'

// The series ordering vocabulary is restated here from the schema, which is its
// one definition: a value the schema gains or loses would otherwise let the site's
// types offer an ordering the data cannot hold, or reject one it does.
describe('SERIES_ORDERINGS', () => {
  it('matches the schema series_ordering enum exactly, in order', () => {
    const schema = JSON.parse(
      readFileSync(new URL('../../../schema/common.schema.json', import.meta.url), 'utf8')
    )
    expect([...SERIES_ORDERINGS]).toEqual(schema.$defs.series_ordering.enum)
  })
})
