import { describe, it, expect } from 'vitest'
import { authoredNote, narratedWorks, narrationNote, workCount } from './person-credits'
import type { Person, WorkCard } from './api'

function work(id: string): WorkCard {
  return { id, title: `Work ${id}`, authors: [] }
}

function credit(id: string, recordingId: string): Person['narrated'][number] {
  return { work: work(id), recording_id: recordingId }
}

describe('workCount', () => {
  it('names the unit and singularizes one', () => {
    expect(workCount(1)).toBe('1 work')
    expect(workCount(0)).toBe('0 works')
    expect(workCount(412)).toBe('412 works')
  })

  it('groups thousands so it reads like the rest of the page', () => {
    expect(workCount(1982)).toBe('1,982 works')
  })
})

describe('narratedWorks', () => {
  it('collapses several recordings of one work to one card, in credit order', () => {
    const works = narratedWorks([credit('a', 'r1'), credit('a', 'r2'), credit('b', 'r3')])
    expect(works.map((w) => w.id)).toEqual(['a', 'b'])
  })

  it('handles an absent list', () => {
    expect(narratedWorks(undefined)).toEqual([])
  })
})

describe('authoredNote', () => {
  it('is absent when the whole list arrived', () => {
    expect(authoredNote(12, 12)).toBeUndefined()
    expect(authoredNote(12, undefined)).toBeUndefined()
  })

  it('states the window in works, the unit the heading also uses', () => {
    expect(authoredNote(500, 1982)).toBe('Showing the first 500 works of 1,982.')
  })
})

describe('narrationNote', () => {
  it('is absent when every credit arrived', () => {
    expect(narrationNote(120, 120, 100)).toBeUndefined()
    expect(narrationNote(120, undefined, 100)).toBeUndefined()
  })

  it('names both units, so a smaller work count beside a larger credit count reads right', () => {
    // The finding: a heading of "412" over a note of "500 of 1982" looked like a
    // contradiction. Both numbers now say what they count.
    const note = narrationNote(500, 1982, 412)
    expect(note).toBe(
      'Showing the first 500 of 1,982 narration credits, which cover the 412 works below.'
    )
    expect(note).toContain('narration credits')
    expect(note).toContain('412 works')
  })
})
