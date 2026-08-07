/**
 * Turns the API's OpenAPI document into a render model for /docs/api.
 *
 * The spec is authored in the Go tree (internal/serve/openapi.json) and served
 * verbatim at /api/v1/openapi.json; this module is the OTHER consumer, imported
 * at build time so the human page and the machine contract are the same
 * document. Nothing here runs in the browser.
 *
 * Deliberately not a general OpenAPI implementation: it resolves LOCAL $refs
 * only, and covers the constructs our own spec uses. A construct we do not
 * emit (remote refs, allOf composition, callbacks) is out of scope rather than
 * half-supported - the drift guard in Go pins the spec's shape, so this side
 * only has to render what that shape allows.
 */

export interface OpenAPISpec {
  openapi: string
  info: { title: string; summary?: string; description?: string; license?: { name: string; url?: string } }
  servers?: { url: string; description?: string }[]
  tags?: { name: string; description?: string }[]
  paths: Record<string, PathItem>
  components?: {
    schemas?: Record<string, Schema>
    parameters?: Record<string, Parameter>
    responses?: Record<string, ResponseObject>
    headers?: Record<string, unknown>
    securitySchemes?: Record<string, unknown>
  }
}

export type PathItem = Record<string, Operation | Parameter[] | undefined>

export interface Operation {
  operationId?: string
  summary?: string
  description?: string
  tags?: string[]
  parameters?: Parameter[]
  requestBody?: { required?: boolean; content?: Record<string, { schema?: Schema }> }
  responses?: Record<string, ResponseObject>
}

export interface Parameter {
  $ref?: string
  name?: string
  in?: string
  required?: boolean
  description?: string
  schema?: Schema
}

export interface ResponseObject {
  $ref?: string
  description?: string
  content?: Record<string, { schema?: Schema }>
}

export interface Schema {
  $ref?: string
  type?: string | string[]
  description?: string
  format?: string
  const?: unknown
  enum?: unknown[]
  default?: unknown
  minimum?: number
  maximum?: number
  minLength?: number
  properties?: Record<string, Schema>
  required?: string[]
  items?: Schema
  oneOf?: Schema[]
  anyOf?: Schema[]
  examples?: unknown[]
}

/** The HTTP methods a path item may carry, in the order they are rendered. */
const METHODS = ['get', 'post', 'put', 'patch', 'delete'] as const

/** How deep a response skeleton expands before it collapses to `{ ... }`. */
const MAX_DEPTH = 8

export interface RenderedParam {
  name: string
  /** 'query' | 'path' | 'header'. */
  location: string
  required: boolean
  /** The rendered type, e.g. `integer` or `"missing" | "has_recaps"`. */
  type: string
  /** Default, bounds and other schema constraints, as one human phrase. */
  constraints: string
  description: string
}

export interface RenderedResponse {
  status: string
  description: string
  /** The JSON skeleton of the body, or null when the response has none. */
  skeleton: string | null
}

export interface RenderedOperation {
  id: string
  /** Upper-case HTTP method. */
  method: string
  path: string
  /** Stable in-page anchor, e.g. `get-api-v1-works-search`. */
  anchor: string
  summary: string
  /** Paragraphs of the operation's description, already split on blank lines. */
  description: string[]
  params: RenderedParam[]
  requestBody: string | null
  responses: RenderedResponse[]
}

export interface RenderedGroup {
  name: string
  description: string
  /** Stable in-page anchor for the group heading. */
  anchor: string
  operations: RenderedOperation[]
}

/** slug turns a name or path into a stable in-page anchor. */
export function slug(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

/**
 * resolveRef follows a local JSON pointer ("#/components/schemas/WorkCard").
 * A non-local or dangling ref returns undefined rather than throwing: a page
 * that renders one shape as empty beats a build that fails on a typo in prose.
 */
export function resolveRef(spec: OpenAPISpec, ref: string): unknown {
  if (!ref.startsWith('#/')) return undefined
  let cur: unknown = spec
  for (const part of ref.slice(2).split('/')) {
    if (typeof cur !== 'object' || cur === null) return undefined
    cur = (cur as Record<string, unknown>)[decodeURIComponent(part.replace(/~1/g, '/').replace(/~0/g, '~'))]
    if (cur === undefined) return undefined
  }
  return cur
}

/**
 * deref resolves a node's `$ref` and merges the sibling keys over the target.
 * OpenAPI 3.1 allows siblings (a `$ref` with its own `description`), and the
 * sibling is the more specific statement, so it wins.
 */
export function deref<T extends { $ref?: string }>(spec: OpenAPISpec, node: T): T {
  if (!node?.$ref) return node
  const target = resolveRef(spec, node.$ref)
  const { $ref: _ref, ...siblings } = node
  if (typeof target !== 'object' || target === null) return siblings as T
  return { ...(target as T), ...(siblings as T) }
}

/** refName is the component name a $ref points at, for union labels. */
function refName(ref: string): string {
  return ref.slice(ref.lastIndexOf('/') + 1)
}

/** typeList normalizes `type` to an array; an absent type is an empty one. */
function typeList(schema: Schema): string[] {
  if (schema.type === undefined) return []
  return Array.isArray(schema.type) ? schema.type : [schema.type]
}

/**
 * scalarLabel renders a non-composite schema as its type label: a `const` and an
 * `enum` render as their literal values, since knowing that `kind` is exactly
 * "work" is the whole point of a discriminator.
 */
function scalarLabel(schema: Schema): string {
  if (schema.const !== undefined) return JSON.stringify(schema.const)
  if (schema.enum?.length) return schema.enum.map((v) => JSON.stringify(v)).join(' | ')
  const types = typeList(schema).filter((t) => t !== 'null')
  return types.length ? types.join(' | ') : 'any'
}

/**
 * splitNullable separates "may be null" from the shape itself, so a nullable
 * object renders as `{ ... } | null` rather than as an unreadable union. It
 * handles both spellings our spec uses: a `type` array containing "null", and a
 * `oneOf` with a `{"type":"null"}` branch.
 */
function splitNullable(schema: Schema): { base: Schema; nullable: boolean } {
  const branches = schema.oneOf ?? schema.anyOf
  if (branches?.length) {
    const real = branches.filter((b) => !typeList(b).includes('null'))
    if (real.length !== branches.length) {
      const rest = { ...schema }
      delete rest.oneOf
      delete rest.anyOf
      return {
        base: real.length === 1 ? { ...rest, ...real[0] } : { ...rest, oneOf: real },
        nullable: true,
      }
    }
  }
  if (typeList(schema).includes('null')) {
    return { base: { ...schema, type: typeList(schema).filter((t) => t !== 'null') }, nullable: true }
  }
  return { base: schema, nullable: false }
}

/**
 * schemaSkeleton renders a schema as an indented, JSON-shaped block whose values
 * are types rather than data. Optional properties (everything not in `required`)
 * carry a trailing `?` on the KEY, which is what a reader needs to know before
 * writing a client.
 *
 * A union of named component schemas renders as `<A | B | C>` rather than
 * expanding every branch: the only such union we emit is the combined search
 * page, whose three branches are each expanded in full under the type-scoped
 * endpoint that returns them.
 */
export function schemaSkeleton(spec: OpenAPISpec, schema: Schema): string {
  return render(spec, schema, 0, 0, [])
}

function render(spec: OpenAPISpec, raw: Schema, indent: number, depth: number, refStack: string[]): string {
  // Refs and nullability interleave: `{"oneOf": [{"$ref": ...}, {"type":
  // "null"}]}` only exposes its $ref once the null branch has been split off, so
  // the two resolutions run alternately until the node stops changing.
  const stack = [...refStack]
  let base = raw
  let nullable = false
  for (;;) {
    if (base.$ref) {
      if (stack.includes(base.$ref)) return `{ ... }${nullable ? ' | null' : ''}`
      stack.push(base.$ref)
      base = deref(spec, base)
      continue
    }
    const split = splitNullable(base)
    if (split.base === base) break
    nullable = nullable || split.nullable
    base = split.base
  }
  const suffix = nullable ? ' | null' : ''

  const branches = base.oneOf ?? base.anyOf
  if (branches?.length) {
    const names = branches.map((b) => (b.$ref ? refName(b.$ref) : scalarLabel(deref(spec, b))))
    return `<${names.join(' | ')}>${suffix}`
  }

  const types = typeList(base)
  const pad = ' '.repeat(indent)

  if (types.includes('array') || base.items) {
    if (depth >= MAX_DEPTH) return `[ ... ]${suffix}`
    const item = base.items ? render(spec, base.items, indent + 2, depth + 1, stack) : 'any'
    return `[\n${pad}  ${item}\n${pad}]${suffix}`
  }

  const props = base.properties
  if (props && Object.keys(props).length) {
    if (depth >= MAX_DEPTH) return `{ ... }${suffix}`
    const required = new Set(base.required ?? [])
    const lines = Object.entries(props).map(([key, value]) => {
      const optional = required.has(key) ? '' : '?'
      return `${pad}  "${key}${optional}": ${render(spec, value, indent + 2, depth + 1, stack)}`
    })
    return `{\n${lines.join(',\n')}\n${pad}}${suffix}`
  }

  if (types.includes('object')) return `{ ... }${suffix}`
  return `${scalarLabel(base)}${suffix}`
}

/**
 * paramConstraints renders a parameter's schema bounds as one phrase, so the
 * table can carry "default 20, 1-50" beside the type instead of three columns
 * that are empty on most rows.
 */
export function paramConstraints(schema: Schema | undefined): string {
  if (!schema) return ''
  const parts: string[] = []
  if (schema.default !== undefined) parts.push(`default ${JSON.stringify(schema.default)}`)
  if (schema.minimum !== undefined && schema.maximum !== undefined) {
    parts.push(`${schema.minimum}-${schema.maximum}`)
  } else if (schema.minimum !== undefined) {
    parts.push(`min ${schema.minimum}`)
  } else if (schema.maximum !== undefined) {
    parts.push(`max ${schema.maximum}`)
  }
  return parts.join(', ')
}

/** One run of a description: either plain prose or an inline code span. */
export interface TextSegment {
  text: string
  code: boolean
}

/**
 * inlineSegments splits a description into plain and `code` runs. OpenAPI
 * descriptions are CommonMark and ours use inline code spans, so the page has to
 * do something with the backticks; splitting into segments the template renders
 * as ELEMENTS means nothing in the spec can ever become markup. An unpaired
 * backtick is left as prose, which is what CommonMark does too.
 */
export function inlineSegments(text: string): TextSegment[] {
  const out: TextSegment[] = []
  const re = /`([^`]+)`/g
  let last = 0
  for (let m = re.exec(text); m !== null; m = re.exec(text)) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), code: false })
    out.push({ text: m[1], code: true })
    last = m.index + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last), code: false })
  return out
}

/** paragraphs splits a description on blank lines, dropping empty runs. */
export function paragraphs(text: string | undefined): string[] {
  if (!text) return []
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
}

function renderParams(spec: OpenAPISpec, params: Parameter[]): RenderedParam[] {
  return params.map((raw) => {
    const p = deref(spec, raw)
    return {
      name: p.name ?? '',
      location: p.in ?? '',
      required: p.required === true,
      type: p.schema ? scalarLabel(splitNullable(deref(spec, p.schema)).base) : '',
      constraints: paramConstraints(p.schema),
      description: p.description ?? '',
    }
  })
}

/** bodySchema picks the JSON schema out of a content map, if there is one. */
function bodySchema(content: Record<string, { schema?: Schema }> | undefined): Schema | undefined {
  if (!content) return undefined
  for (const [mediaType, entry] of Object.entries(content)) {
    if (mediaType.includes('json') && entry.schema) return entry.schema
  }
  return undefined
}

function renderResponses(spec: OpenAPISpec, responses: Record<string, ResponseObject>): RenderedResponse[] {
  return Object.entries(responses)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([status, raw]) => {
      const res = deref(spec, raw)
      const schema = bodySchema(res.content)
      return {
        status,
        description: res.description ?? '',
        skeleton: schema ? schemaSkeleton(spec, schema) : null,
      }
    })
}

/**
 * groupOperations turns the spec into the page's sections: one group per tag, in
 * the order the spec declares its tags, each holding its operations in the order
 * the paths appear. A tag used by an operation but not declared in `tags` still
 * gets a group (appended, alphabetically) rather than vanishing from the page.
 */
export function groupOperations(spec: OpenAPISpec): RenderedGroup[] {
  const byTag = new Map<string, RenderedOperation[]>()

  for (const [path, item] of Object.entries(spec.paths ?? {})) {
    const shared = (item.parameters as Parameter[] | undefined) ?? []
    for (const method of METHODS) {
      const op = item[method] as Operation | undefined
      if (!op) continue
      const rendered: RenderedOperation = {
        id: op.operationId ?? `${method}-${path}`,
        method: method.toUpperCase(),
        path,
        anchor: slug(`${method}-${path}`),
        summary: op.summary ?? '',
        description: paragraphs(op.description),
        params: renderParams(spec, [...shared, ...(op.parameters ?? [])]),
        requestBody: (() => {
          const schema = bodySchema(op.requestBody?.content)
          return schema ? schemaSkeleton(spec, schema) : null
        })(),
        responses: renderResponses(spec, op.responses ?? {}),
      }
      for (const tag of op.tags?.length ? op.tags : ['Other']) {
        const bucket = byTag.get(tag)
        if (bucket) bucket.push(rendered)
        else byTag.set(tag, [rendered])
      }
    }
  }

  const declared = (spec.tags ?? []).map((t) => t.name)
  const extra = [...byTag.keys()].filter((t) => !declared.includes(t)).sort()
  const described = new Map((spec.tags ?? []).map((t) => [t.name, t.description ?? '']))

  return [...declared, ...extra]
    .filter((name) => byTag.has(name))
    .map((name) => ({
      name,
      description: described.get(name) ?? '',
      anchor: slug(name),
      operations: byTag.get(name) ?? [],
    }))
}
