type MarkdownPosition = {
  start?: { offset?: number }
  end?: { offset?: number }
}

type MarkdownNode = {
  type: string
  value?: string
  url?: string
  children?: MarkdownNode[]
  position?: MarkdownPosition
}

type MarkdownFile = { value?: string | Uint8Array }

// CommonMark intentionally leaves `**标题：**正文` as text because punctuation before
// the closing delimiter is followed by a letter. Models produce this form frequently,
// so repair only unresolved plain-text nodes after parsing. Code, math, HTML and link
// destinations are different AST node types and therefore remain byte-for-byte intact.
export function remarkRepairModelMarkdown() {
  return (tree: MarkdownNode, file: MarkdownFile) => {
    const source = typeof file.value === 'string' ? file.value : ''
    repairChildren(tree, source)
  }
}

function repairChildren(parent: MarkdownNode, source: string) {
  if (!parent.children) return
  // Autolinks use their URL as visible text. Treat the whole link as protected so
  // marker-like URL characters never become presentation formatting.
  if (parent.type === 'link' && parent.children.length === 1 && parent.children[0].type === 'text' && parent.children[0].value === parent.url) return
  const children: MarkdownNode[] = []
  for (const child of parent.children) {
    if (child.type === 'text' && child.value && isLiteralSource(child, source)) {
      const repaired = repairPlainText(child.value)
      if (repaired) {
        children.push(...repaired)
        continue
      }
    }
    repairChildren(child, source)
    children.push(child)
  }
  parent.children = children
}

// The parser has already consumed valid emphasis, so markers reaching this function
// are unresolved text. Keep the repair deliberately narrow: paired ** or __, label
// ending in punctuation/symbol, and prose beginning immediately after the pair.
const malformedStrong = /(?<![\\*])(\*\*)(?!\*)([^*\n]+?[\p{P}\p{S}])\1(?=[\p{L}\p{N}])|(?<![\\_])(__)(?!_)([^_\n]+?[\p{P}\p{S}])\3(?=[\p{L}\p{N}])/gu

function repairPlainText(value: string): MarkdownNode[] | null {
  malformedStrong.lastIndex = 0
  const nodes: MarkdownNode[] = []
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = malformedStrong.exec(value)) !== null) {
    if (match.index > cursor) nodes.push({ type: 'text', value: value.slice(cursor, match.index) })
    nodes.push({ type: 'strong', children: [{ type: 'text', value: match[2] || match[4] }] })
    cursor = match.index + match[0].length
  }
  if (nodes.length === 0) return null
  if (cursor < value.length) nodes.push({ type: 'text', value: value.slice(cursor) })
  return nodes
}

// Parsing can unescape source text (`\\*` -> `*`). Skip those nodes so an explicit
// escape remains literal instead of being mistaken for malformed model Markdown.
function isLiteralSource(node: MarkdownNode, source: string) {
  const start = node.position?.start?.offset
  const end = node.position?.end?.offset
  if (start === undefined || end === undefined) return false
  return source.slice(start, end) === node.value
}
