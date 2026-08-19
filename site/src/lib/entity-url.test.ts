import { describe, it, expect } from 'vitest'
import { entitySlugFromLocation, workGuideSlugFromLocation } from './entity-url'

describe('entitySlugFromLocation - the path route', () => {
  it('reads the slug from each kind\'s own segment', () => {
    expect(entitySlugFromLocation('/works/project-hail-mary', '', 'work')).toBe(
      'project-hail-mary'
    )
    expect(entitySlugFromLocation('/people/andy-weir', '', 'person')).toBe('andy-weir')
    expect(entitySlugFromLocation('/series/the-stormlight-archive', '', 'series')).toBe(
      'the-stormlight-archive'
    )
  })

  it('reads person pages from /people, not /persons', () => {
    expect(entitySlugFromLocation('/persons/andy-weir', '', 'person')).toBeNull()
    expect(entitySlugFromLocation('/person/andy-weir', '', 'person')).toBeNull()
  })

  it('does not read another kind\'s path', () => {
    expect(entitySlugFromLocation('/people/andy-weir', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/works/project-hail-mary', '', 'series')).toBeNull()
  })

  it('percent-decodes the slug', () => {
    expect(entitySlugFromLocation('/works/a%20b%2Fc', '', 'work')).toBe('a b/c')
  })

  it('ignores a malformed escape rather than passing it on', () => {
    expect(entitySlugFromLocation('/works/%', '', 'work')).toBeNull()
  })

  it('wins over a legacy ?id= on the same URL', () => {
    expect(entitySlugFromLocation('/works/from-path', '?id=from-query', 'work')).toBe('from-path')
  })
})

describe('entitySlugFromLocation - strict path matching', () => {
  it('matches nothing with trailing junk', () => {
    expect(entitySlugFromLocation('/works/a/b', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/works/a/', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/x/works/a', '', 'work')).toBeNull()
  })

  it('matches nothing with no slug at all', () => {
    expect(entitySlugFromLocation('/works', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/works/', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/', '', 'work')).toBeNull()
  })

  it('does not match a segment that merely starts with the namespace', () => {
    expect(entitySlugFromLocation('/worksheets/a', '', 'work')).toBeNull()
  })
})

describe('entitySlugFromLocation - the legacy query fallback', () => {
  it('reads ?id= on the legacy shell', () => {
    expect(entitySlugFromLocation('/work', '?id=project-hail-mary', 'work')).toBe(
      'project-hail-mary'
    )
    expect(entitySlugFromLocation('/person', '?id=andy-weir', 'person')).toBe('andy-weir')
    expect(entitySlugFromLocation('/series', '?id=stormlight', 'series')).toBe('stormlight')
  })

  it('decodes the query value', () => {
    expect(entitySlugFromLocation('/work', '?id=a%20b%2Fc', 'work')).toBe('a b/c')
  })

  it('reads ?id= regardless of parameter order', () => {
    expect(entitySlugFromLocation('/work', '?tab=chapters&id=x', 'work')).toBe('x')
  })

  it('distinguishes a blank id from an absent one', () => {
    expect(entitySlugFromLocation('/work', '?id=', 'work')).toBe('')
    expect(entitySlugFromLocation('/work', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/work', '?tab=chapters', 'work')).toBeNull()
  })

  it('tolerates a search string with no leading question mark', () => {
    expect(entitySlugFromLocation('/work', 'id=x', 'work')).toBe('x')
  })
})

describe('workGuideSlugFromLocation - the guide sub-pages', () => {
  it('reads the work slug from each guide path', () => {
    expect(workGuideSlugFromLocation('/works/killing-floor/recap', '', 'recap')).toBe(
      'killing-floor'
    )
    expect(workGuideSlugFromLocation('/works/killing-floor/characters', '', 'characters')).toBe(
      'killing-floor'
    )
  })

  it('does not read the other guide\'s path', () => {
    expect(workGuideSlugFromLocation('/works/a/recap', '', 'characters')).toBeNull()
    expect(workGuideSlugFromLocation('/works/a/characters', '', 'recap')).toBeNull()
  })

  it('matches nothing outside the exact four-segment shape', () => {
    expect(workGuideSlugFromLocation('/works/a', '', 'recap')).toBeNull()
    expect(workGuideSlugFromLocation('/works/a/recap/', '', 'recap')).toBeNull()
    expect(workGuideSlugFromLocation('/works/a/b/recap', '', 'recap')).toBeNull()
    expect(workGuideSlugFromLocation('/works//recap', '', 'recap')).toBeNull()
    expect(workGuideSlugFromLocation('/people/a/recap', '', 'recap')).toBeNull()
    expect(workGuideSlugFromLocation('/x/works/a/recap', '', 'recap')).toBeNull()
  })

  it('percent-decodes the slug and drops a malformed escape', () => {
    expect(workGuideSlugFromLocation('/works/a%20b/recap', '', 'recap')).toBe('a b')
    expect(workGuideSlugFromLocation('/works/%/recap', '', 'recap')).toBeNull()
  })

  it('falls back to ?id= on the flat shell, which is what astro dev serves', () => {
    expect(workGuideSlugFromLocation('/recap', '?id=killing-floor', 'recap')).toBe('killing-floor')
    expect(workGuideSlugFromLocation('/characters', '?id=killing-floor', 'characters')).toBe(
      'killing-floor'
    )
    expect(workGuideSlugFromLocation('/recap', '?id=', 'recap')).toBe('')
    expect(workGuideSlugFromLocation('/recap', '', 'recap')).toBeNull()
  })

  it('prefers the path over a legacy ?id= on the same URL', () => {
    expect(workGuideSlugFromLocation('/works/from-path/recap', '?id=from-query', 'recap')).toBe(
      'from-path'
    )
  })

  it('never reads the work page itself as a guide page', () => {
    expect(workGuideSlugFromLocation('/works/a', '', 'recap')).toBeNull()
    // ...and the strict work parser still refuses the guide paths, which is the
    // pair of rules that keeps the two shells' islands off each other's URLs.
    expect(entitySlugFromLocation('/works/a/recap', '', 'work')).toBeNull()
    expect(entitySlugFromLocation('/works/a/characters', '', 'work')).toBeNull()
  })
})
