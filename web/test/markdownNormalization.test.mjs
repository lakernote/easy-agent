import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { transform } from 'esbuild'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

const source = await readFile(new URL('../src/markdownNormalization.ts', import.meta.url), 'utf8')
const compiled = await transform(source, { format: 'esm', loader: 'ts', target: 'es2022' })
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.code).toString('base64')}`
const { normalizeAssistantMarkdown } = await import(moduleURL)
const renderMarkdown = (content) => renderToStaticMarkup(React.createElement(ReactMarkdown, { children: content, remarkPlugins: [remarkGfm] }))

test('repairs strong emphasis followed directly by prose', () => {
  const input = '- **今天 9 月 12 日：**多云\n\n**出行建议：**两天适合出行。'
  const normalized = normalizeAssistantMarkdown(input)
  assert.equal(normalized, '- **今天 9 月 12 日：** 多云\n\n**出行建议：** 两天适合出行。')
  const rendered = renderMarkdown(normalized)
  assert.match(rendered, /<strong>今天 9 月 12 日：<\/strong> 多云/)
  assert.match(rendered, /<strong>出行建议：<\/strong> 两天适合出行/)
  assert.doesNotMatch(rendered, /\*\*/)
})

test('keeps valid emphasis and code content unchanged', () => {
  const input = '**普通加粗**文字，**20～27℃**；降雨。\n`**标题：**正文`\n```md\n**标题：**正文\n```'
  assert.equal(normalizeAssistantMarkdown(input), input)
})

test('does not confuse one closing delimiter with the next opening delimiter', () => {
  const input = '- **今天（12 日）**：多云，**20～27℃**；白天降雨概率约 **40%**，夜间局部时段接近 **50%**。\n- **明天（13 日）**：阴天，**20～28℃**；午后降雨概率约 **40%**。'
  const normalized = normalizeAssistantMarkdown(input)
  assert.equal(normalized, input)
  const rendered = renderMarkdown(normalized)
  assert.match(rendered, /<strong>20～27℃<\/strong>/)
  assert.match(rendered, /<strong>20～28℃<\/strong>/)
  assert.doesNotMatch(rendered, /\*\*/)
})

test('is idempotent and supports underscore emphasis', () => {
  const repaired = '__注意：__ 后续内容'
  assert.equal(normalizeAssistantMarkdown('__注意：__后续内容'), repaired)
  assert.equal(normalizeAssistantMarkdown(repaired), repaired)
})
