import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { transform } from 'esbuild'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'

const source = await readFile(new URL('../src/remarkRepairModelMarkdown.ts', import.meta.url), 'utf8')
const compiled = await transform(source, { format: 'esm', loader: 'ts', target: 'es2022' })
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.code).toString('base64')}`
const { remarkRepairModelMarkdown } = await import(moduleURL)
const renderMarkdown = (content, plugins = [remarkGfm]) => renderToStaticMarkup(React.createElement(ReactMarkdown, { children: content, remarkPlugins: plugins }))
const renderRepaired = (content, plugins = [remarkGfm]) => renderMarkdown(content, [...plugins, remarkRepairModelMarkdown])

test('repairs malformed strong emphasis without changing Chinese spacing', () => {
  const input = '- **今天 9 月 12 日：**多云\n\n**出行建议：**两天适合出行。'
  const rendered = renderRepaired(input)
  assert.match(rendered, /<strong>今天 9 月 12 日：<\/strong>多云/)
  assert.match(rendered, /<strong>出行建议：<\/strong>两天适合出行/)
  assert.doesNotMatch(rendered, /\*\*/)
  assert.doesNotMatch(rendered, /：<\/strong> /)
})

test('does not confuse one closing delimiter with the next opening delimiter', () => {
  const input = '- **今天（12 日）**：多云，**20～27℃**；白天降雨概率约 **40%**，夜间局部时段接近 **50%**。\n- **明天（13 日）**：阴天，**20～28℃**；午后降雨概率约 **40%**。'
  const rendered = renderRepaired(input)
  assert.equal(rendered, renderMarkdown(input))
  assert.match(rendered, /<strong>20～27℃<\/strong>/)
  assert.match(rendered, /<strong>20～28℃<\/strong>/)
  assert.doesNotMatch(rendered, /\*\*/)
})

test('repairs multiple star and underscore labels in one text node', () => {
  const rendered = renderRepaired('**结论：**可用；__注意：__需要测试。')
  assert.match(rendered, /<strong>结论：<\/strong>可用；<strong>注意：<\/strong>需要测试/)
})

test('leaves code, math, link destinations and HTML unchanged', () => {
  const cases = [
    '    **标题：**正文',
    '`**标题：**正文`',
    '```md\n**标题：**正文\n```',
    '$x**标题：**正文$',
    '[链接](https://example.com/**标题：**正文)',
    '<https://example.com/**标题：**正文>',
    '<span data-x="**标题：**正文">ok</span>',
  ]
  for (const input of cases) {
    assert.equal(renderRepaired(input, [remarkGfm, remarkMath]), renderMarkdown(input, [remarkGfm, remarkMath]), input)
  }
})

test('respects explicitly escaped emphasis markers', () => {
  const input = '\\**标题：**正文'
  assert.equal(renderRepaired(input), renderMarkdown(input))
})

test('can repair a human-readable link label without touching its destination', () => {
  const rendered = renderRepaired('[**来源：**官方文档](https://example.com/**raw**path)')
  assert.match(rendered, /^<p><a href="https:\/\/example\.com\/\*\*raw\*\*path"><strong>来源：<\/strong>官方文档<\/a><\/p>$/)
})
