import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { RecapsPanel } from '../components/detail/expressive-panels'

it('keeps every recap body inside a supported snippet-exclusion element', () => {
  const rows = [
    { title: 'In short', text: 'Summary spoiler.', wholeBook: true },
    { title: 'Through chapter 3', text: 'Chapter spoiler.' },
    { title: 'How did it end?', text: 'Ending spoiler.', wholeBook: true },
  ]
  const markup = renderToStaticMarkup(createElement(RecapsPanel, { rows }))
  // Google supports data-nosnippet only on div, span and section. A closed
  // disclosure alone does not protect its body from search-result snippets.
  const protectedBodies = [...markup.matchAll(/<(div|span|section)\b[^>]*\bdata-nosnippet[^>]*>([\s\S]*?)<\/\1>/g)]
    .map((match) => match[2])
  for (const row of rows) {
    expect(protectedBodies.some((body) => body.includes(row.text)), row.title).toBe(true)
  }
})
