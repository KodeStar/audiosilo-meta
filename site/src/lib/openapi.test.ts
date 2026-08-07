import { describe, it, expect } from 'vitest'
import realSpec from '../../../internal/serve/openapi.json'
import {
  deref,
  groupOperations,
  inlineSegments,
  paragraphs,
  paramConstraints,
  resolveRef,
  schemaSkeleton,
  slug,
  type OpenAPISpec,
  type Schema,
} from './openapi'

/** A miniature spec exercising every construct the real one uses. */
const spec: OpenAPISpec = {
  openapi: '3.1.0',
  info: { title: 'Test API' },
  tags: [
    { name: 'Search', description: 'Finding things.' },
    { name: 'Server', description: 'Housekeeping.' },
  ],
  paths: {
    '/search': {
      get: {
        operationId: 'search',
        summary: 'Search',
        description: 'First para.\n\nSecond para.',
        tags: ['Search'],
        parameters: [
          { $ref: '#/components/parameters/Q' },
          {
            name: 'limit',
            in: 'query',
            schema: { type: 'integer', default: 20, minimum: 1, maximum: 50 },
          },
        ],
        responses: {
          '400': { $ref: '#/components/responses/BadRequest' },
          '200': {
            description: 'Hits.',
            content: {
              'application/json': { schema: { $ref: '#/components/schemas/Results' } },
            },
          },
        },
      },
    },
    '/healthz': {
      get: { operationId: 'healthz', summary: 'Health', tags: ['Server'], responses: { '204': { description: 'Fine.' } } },
    },
    '/legacy': {
      get: { operationId: 'legacy', summary: 'Legacy', responses: { '200': { description: 'ok' } } },
    },
  },
  components: {
    parameters: {
      Q: { name: 'q', in: 'query', required: true, description: 'The query.', schema: { type: 'string' } },
    },
    responses: {
      BadRequest: {
        description: 'Bad.',
        content: { 'application/json': { schema: { $ref: '#/components/schemas/Error' } } },
      },
    },
    schemas: {
      Error: { type: 'object', required: ['error'], properties: { error: { type: 'string' } } },
      Ref: { type: 'object', required: ['id'], properties: { id: { type: 'string' } } },
      Results: {
        type: 'object',
        required: ['results'],
        properties: {
          results: { type: 'array', items: { $ref: '#/components/schemas/Hit' } },
        },
      },
      Composed: {
        allOf: [
          { $ref: '#/components/schemas/Ref' },
          {
            type: 'object',
            required: ['kind'],
            properties: { kind: { type: 'string', const: 'work' }, note: { type: 'string' } },
          },
        ],
      },
      Hit: {
        type: 'object',
        required: ['kind', 'id', 'series'],
        properties: {
          kind: { type: 'string', const: 'work' },
          id: { type: 'string' },
          note: { type: 'string' },
          count: { type: ['integer', 'null'] },
          series: { oneOf: [{ $ref: '#/components/schemas/Ref' }, { type: 'null' }] },
          either: { oneOf: [{ $ref: '#/components/schemas/Ref' }, { $ref: '#/components/schemas/Error' }] },
          filter: { type: 'string', enum: ['missing', 'has_recaps'] },
        },
      },
    },
  },
}

describe('slug', () => {
  it('makes a stable anchor out of a method and path', () => {
    expect(slug('get-/api/v1/works/search')).toBe('get-api-v1-works-search')
  })
  it('drops the braces of a path template', () => {
    expect(slug('get-/api/v1/works/{id}')).toBe('get-api-v1-works-id')
  })
})

describe('resolveRef', () => {
  it('follows a local pointer', () => {
    expect(resolveRef(spec, '#/components/schemas/Error')).toEqual(spec.components!.schemas!.Error)
  })
  it('returns undefined for a dangling pointer', () => {
    expect(resolveRef(spec, '#/components/schemas/Nope')).toBeUndefined()
  })
  it('returns undefined for a remote ref', () => {
    expect(resolveRef(spec, 'other.json#/x')).toBeUndefined()
  })
})

describe('deref', () => {
  it('passes a node with no $ref through untouched', () => {
    const node: Schema = { type: 'string' }
    expect(deref(spec, node)).toBe(node)
  })
  it('lets a sibling key win over the target it refs', () => {
    const merged = deref<Schema>(spec, { $ref: '#/components/schemas/Error', description: 'local' })
    expect(merged.description).toBe('local')
    expect(merged.type).toBe('object')
  })
})

describe('schemaSkeleton', () => {
  const out = schemaSkeleton(spec, { $ref: '#/components/schemas/Results' })

  it('marks optional properties with a trailing ? on the key', () => {
    expect(out).toContain('"id": string')
    expect(out).toContain('"note?": string')
  })
  it('renders a const as its literal value', () => {
    expect(out).toContain('"kind": "work"')
  })
  it('renders an enum as its alternatives', () => {
    expect(out).toContain('"filter?": "missing" | "has_recaps"')
  })
  it('renders a nullable type as `| null` rather than a union with null', () => {
    expect(out).toContain('"count?": integer | null')
  })
  it('expands a nullable object and marks it nullable', () => {
    expect(out).toContain('"series": {')
    expect(out).toMatch(/\}\s*\| null/)
  })
  it('names the branches of a real union instead of expanding them', () => {
    expect(out).toContain('"either?": <Ref | Error>')
  })
  it('indents nested objects inside an array', () => {
    expect(out).toBe(`{
  "results": [
    {
      "kind": "work",
      "id": string,
      "note?": string,
      "count?": integer | null,
      "series": {
        "id": string
      } | null,
      "either?": <Ref | Error>,
      "filter?": "missing" | "has_recaps"
    }
  ]
}`)
  })
  it('renders a bare scalar schema', () => {
    expect(schemaSkeleton(spec, { type: 'string' })).toBe('string')
  })
  it('renders an object with no declared properties as an opaque block', () => {
    expect(schemaSkeleton(spec, { type: 'object' })).toBe('{ ... }')
  })
  it('flattens an allOf composition into one shape, required lists unioned', () => {
    expect(schemaSkeleton(spec, { $ref: '#/components/schemas/Composed' })).toBe(`{
  "id": string,
  "kind": "work",
  "note?": string
}`)
  })
})

describe('paramConstraints', () => {
  it('renders a default and a range together', () => {
    expect(paramConstraints({ type: 'integer', default: 20, minimum: 1, maximum: 50 })).toBe('default 20, 1-50')
  })
  it('renders a lone bound', () => {
    expect(paramConstraints({ type: 'integer', minimum: 0 })).toBe('min 0')
    expect(paramConstraints({ type: 'integer', maximum: 9 })).toBe('max 9')
  })
  it('is empty for an unconstrained schema', () => {
    expect(paramConstraints({ type: 'string' })).toBe('')
    expect(paramConstraints(undefined)).toBe('')
  })
})

describe('inlineSegments', () => {
  it('splits code spans out of prose', () => {
    expect(inlineSegments('sets `kind` to "work" always')).toEqual([
      { text: 'sets ', code: false },
      { text: 'kind', code: true },
      { text: ' to "work" always', code: false },
    ])
  })
  it('handles a leading and a trailing span', () => {
    expect(inlineSegments('`a` and `b`')).toEqual([
      { text: 'a', code: true },
      { text: ' and ', code: false },
      { text: 'b', code: true },
    ])
  })
  it('leaves an unpaired backtick as prose', () => {
    expect(inlineSegments('a ` b')).toEqual([{ text: 'a ` b', code: false }])
  })
  it('is empty for empty text', () => {
    expect(inlineSegments('')).toEqual([])
  })
})

describe('paragraphs', () => {
  it('splits on blank lines', () => {
    expect(paragraphs('one\n\ntwo')).toEqual(['one', 'two'])
  })
  it('is empty for an absent description', () => {
    expect(paragraphs(undefined)).toEqual([])
  })
})

describe('groupOperations', () => {
  const groups = groupOperations(spec)

  it('keeps the tag order the spec declares, then appends undeclared tags', () => {
    expect(groups.map((g) => g.name)).toEqual(['Search', 'Server', 'Other'])
  })
  it('carries the tag description onto the group', () => {
    expect(groups[0].description).toBe('Finding things.')
  })
  it('renders the operation with its method, anchor and paragraphs', () => {
    const op = groups[0].operations[0]
    expect(op.method).toBe('GET')
    expect(op.path).toBe('/search')
    expect(op.anchor).toBe('get-search')
    expect(op.description).toEqual(['First para.', 'Second para.'])
  })
  it('resolves a $ref parameter and reports its constraints', () => {
    const [q, limit] = groups[0].operations[0].params
    expect(q).toMatchObject({ name: 'q', location: 'query', required: true, type: 'string' })
    expect(q.description).toBe('The query.')
    expect(limit).toMatchObject({ name: 'limit', required: false, constraints: 'default 20, 1-50' })
  })
  it('sorts responses by status and resolves a $ref response', () => {
    const responses = groups[0].operations[0].responses
    expect(responses.map((r) => r.status)).toEqual(['200', '400'])
    expect(responses[1].description).toBe('Bad.')
    expect(responses[1].skeleton).toContain('"error": string')
  })
  it('leaves a bodiless response with a null skeleton', () => {
    expect(groups[1].operations[0].responses[0].skeleton).toBeNull()
  })
})

// The real spec is the page's actual input, so a smoke pass over it catches a
// spec edit that renders as nothing long before anyone looks at the page.
describe('the shipped spec', () => {
  const groups = groupOperations(realSpec as unknown as OpenAPISpec)

  it('renders every operation into a declared group', () => {
    expect(groups.length).toBeGreaterThan(0)
    expect(groups.map((g) => g.name)).not.toContain('Other')
  })
  it('produces unique anchors', () => {
    const anchors = groups.flatMap((g) => g.operations.map((o) => o.anchor))
    expect(new Set(anchors).size).toBe(anchors.length)
  })
  it('never leaves an unresolved $ref in a rendered skeleton', () => {
    for (const group of groups) {
      for (const op of group.operations) {
        for (const res of op.responses) {
          expect(res.skeleton ?? '', `${op.method} ${op.path} ${res.status}`).not.toContain('$ref')
        }
      }
    }
  })
  it('renders the work search page shape, inherited card fields included', () => {
    const works = groups
      .flatMap((g) => g.operations)
      .find((o) => o.path === '/api/v1/works/search')
    const skeleton = works?.responses.find((r) => r.status === '200')?.skeleton
    expect(skeleton).toContain('"narrators": [')
    // WorkResult composes WorkCard through allOf, so a card field appearing here
    // is what proves the composition was flattened rather than dropped.
    expect(skeleton).toContain('"cover_url": string | null')
    expect(skeleton).toContain('"kind": "work"')
  })
})
