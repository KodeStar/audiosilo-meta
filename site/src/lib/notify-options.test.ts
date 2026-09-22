import { describe, it, expect } from 'vitest'
import {
  NOTIFY_INTRO_NOTES,
  NOTIFY_OPTIONS,
  notifyDocHref,
  type NotifyOption,
} from './notify-options'

/** Every string the module publishes, flattened - the copy rules below are
    about all of it, not about one field. */
function everyString(): string[] {
  const out: string[] = [...NOTIFY_INTRO_NOTES]
  for (const option of NOTIFY_OPTIONS) {
    out.push(option.id, option.label, option.blurb, ...option.steps, ...(option.notes ?? []))
  }
  return out
}

function byID(id: NotifyOption['id']): NotifyOption {
  const option = NOTIFY_OPTIONS.find((candidate) => candidate.id === id)
  if (!option) throw new Error(`no option ${id}`)
  return option
}

describe('NOTIFY_OPTIONS', () => {
  it('has unique ids', () => {
    const ids = NOTIFY_OPTIONS.map((option) => option.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('has anchor-safe ids, because the id IS the docs page anchor', () => {
    for (const option of NOTIFY_OPTIONS) expect(option.id).toMatch(/^[a-z-]+$/)
  })

  it('leads with the option that needs no account and no app', () => {
    expect(NOTIFY_OPTIONS[0].id).toBe('calendar')
    expect(NOTIFY_OPTIONS[0].needsAccount).toBe(false)
  })

  it('gives every option a blurb for the picker and at least two concrete steps', () => {
    for (const option of NOTIFY_OPTIONS) {
      expect(option.blurb.trim().length, option.id).toBeGreaterThan(0)
      expect(option.steps.length, option.id).toBeGreaterThanOrEqual(2)
      for (const step of option.steps) expect(step.trim().length, option.id).toBeGreaterThan(0)
    }
  })

  it('says which URL each set of steps pastes', () => {
    expect(byID('calendar').feed).toBe('webcal')
    for (const option of NOTIFY_OPTIONS) {
      expect(['webcal', 'atom', 'json'], option.id).toContain(option.feed)
    }
  })

  it('is honest about a third-party account', () => {
    // Blogtrottr takes an address without one, so email is false with the two
    // options above it; the four below it all sign you in somewhere.
    for (const id of ['calendar', 'rss-app', 'email', 'self-hosted'] as const) {
      expect(byID(id).needsAccount, id).toBe(false)
    }
    for (const id of ['telegram', 'slack', 'discord', 'automation'] as const) {
      expect(byID(id).needsAccount, id).toBe(true)
    }
  })

  it('keeps the simple account-free options first, self-hosted being the deliberate exception', () => {
    const firstAccount = NOTIFY_OPTIONS.findIndex((option) => option.needsAccount)
    const lastFree = NOTIFY_OPTIONS.map((option) => option.needsAccount).lastIndexOf(false)
    // self-hosted is the one deliberate exception: it needs no account, but it
    // needs a server, so it sits with the advanced options at the end.
    expect(NOTIFY_OPTIONS[lastFree].id).toBe('self-hosted')
    expect(firstAccount).toBeGreaterThan(0)
  })

  it('keeps the intro notes non-empty, since the docs page renders them as the standing caveats', () => {
    expect(NOTIFY_INTRO_NOTES.length).toBeGreaterThanOrEqual(3)
    for (const note of NOTIFY_INTRO_NOTES) expect(note.trim().length).toBeGreaterThan(0)
  })

  it('uses hyphens only - no em dash or en dash anywhere in the copy', () => {
    for (const text of everyString()) {
      expect(text, text).not.toMatch(/[\u2013\u2014]/)
    }
  })
})

describe('notifyDocHref', () => {
  it('points at the option section of the docs page', () => {
    expect(notifyDocHref('calendar')).toBe('/docs/notifications#calendar')
    expect(notifyDocHref('self-hosted')).toBe('/docs/notifications#self-hosted')
  })

  it('resolves for every option', () => {
    for (const option of NOTIFY_OPTIONS) {
      expect(notifyDocHref(option.id)).toBe(`/docs/notifications#${option.id}`)
    }
  })
})
