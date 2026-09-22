import { describe, it, expect } from 'vitest'
import { hashForWatchTab, watchTabFromHash, type WatchTab } from './watchnav'

const TABS: WatchTab[] = ['available', 'all', 'notify', 'import']

describe('hashForWatchTab', () => {
  it('clears the hash for the default tab and names the rest', () => {
    expect(hashForWatchTab('available')).toBe('')
    expect(hashForWatchTab('all')).toBe('#all')
    expect(hashForWatchTab('notify')).toBe('#notify')
    expect(hashForWatchTab('import')).toBe('#import')
  })
})

describe('watchTabFromHash', () => {
  it('maps an empty hash to available', () => {
    expect(watchTabFromHash('')).toBe('available')
    expect(watchTabFromHash('#')).toBe('available')
  })
  it('maps the known hashes', () => {
    expect(watchTabFromHash('#all')).toBe('all')
    expect(watchTabFromHash('#notify')).toBe('notify')
    expect(watchTabFromHash('#import')).toBe('import')
  })
  it('tolerates a missing leading hash', () => {
    expect(watchTabFromHash('notify')).toBe('notify')
  })
  it('falls back to available for an unknown hash', () => {
    expect(watchTabFromHash('#nope')).toBe('available')
    expect(watchTabFromHash('#available')).toBe('available')
    expect(watchTabFromHash('#ALL')).toBe('available')
  })
})

describe('round trip', () => {
  it('every tab survives hash -> tab', () => {
    for (const tab of TABS) expect(watchTabFromHash(hashForWatchTab(tab))).toBe(tab)
  })
})
