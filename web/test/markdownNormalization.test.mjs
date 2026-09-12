import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { transform } from 'esbuild'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'

const source = await readFile(new URL('../src/markdownNormalization.ts', import.meta.url), 'utf8')
const compiled = await transform(source, { format: 'esm', loader: 'ts', target: 'es2022' })
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.code).toString('base64')}`
const { normalizeAssistantMarkdown } = await import(moduleURL)

test('repairs strong emphasis followed directly by prose', () => {
  const input = '- **今天 9 月 12 日：**多云\n\n**出行建议：**两天适合出行。'
  const normalized = normalizeAssistantMarkdown(input)
  assert.equal(normalized, '- **今天 9 月 12 日：** 多云\n\n**出行建议：** 两天适合出行。')
  const rendered = renderToStaticMarkup(React.createElement(ReactMarkdown, { children: normalized }))
  assert.match(rendered, /<strong>今天 9 月 12 日：<\/strong> 多云/)
  assert.match(rendered, /<strong>出行建议：<\/strong> 两天适合出行/)
  assert.doesNotMatch(rendered, /\*\*/)
})

test('keeps valid emphasis and code content unchanged', () => {
  const input = '**普通加粗**文字，**20～27℃**；降雨。\n`**标题：**正文`\n```md\n**标题：**正文\n```'
  assert.equal(normalizeAssistantMarkdown(input), input)
})

test('is idempotent and supports underscore emphasis', () => {
  const repaired = '__注意：__ 后续内容'
  assert.equal(normalizeAssistantMarkdown('__注意：__后续内容'), repaired)
  assert.equal(normalizeAssistantMarkdown(repaired), repaired)
})
