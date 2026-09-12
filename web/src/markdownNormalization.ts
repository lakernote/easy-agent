// CommonMark 不允许强强调的闭合符同时满足“前一个字符是标点/符号”且
// “后一个字符是正文”。模型常生成 **标题：**正文，这会把星号原样显示。
// 这里只在普通 Markdown 文本中补分隔空格；代码围栏和行内代码保持原样。
export function normalizeAssistantMarkdown(content: string) {
  const parts = content.split(/(\r?\n)/)
  let fence: { marker: string; length: number } | null = null
  return parts.map((part) => {
    if (part === '\n' || part === '\r\n') return part
    const fenceRun = part.match(/^[ \t]{0,3}(`{3,}|~{3,})/)
    if (fence) {
      if (fenceRun && fenceRun[1][0] === fence.marker && fenceRun[1].length >= fence.length && /^[ \t]{0,3}(`{3,}|~{3,})[ \t]*$/.test(part)) fence = null
      return part
    }
    if (fenceRun) {
      fence = { marker: fenceRun[1][0], length: fenceRun[1].length }
      return part
    }
    return normalizeOutsideInlineCode(part)
  }).join('')
}

function normalizeOutsideInlineCode(line: string) {
  let result = ''
  let plainStart = 0
  let index = 0
  while (index < line.length) {
    if (line[index] !== '`') {
      index++
      continue
    }
    const openingStart = index
    while (line[index] === '`') index++
    const markerLength = index - openingStart
    const closingStart = findBacktickRun(line, index, markerLength)
    result += normalizeStrongBoundary(line.slice(plainStart, openingStart))
    if (closingStart < 0) return result + line.slice(openingStart)
    const closingEnd = closingStart + markerLength
    result += line.slice(openingStart, closingEnd)
    index = closingEnd
    plainStart = closingEnd
  }
  return result + normalizeStrongBoundary(line.slice(plainStart))
}

function findBacktickRun(value: string, start: number, length: number) {
  let index = start
  while (index < value.length) {
    if (value[index] !== '`') {
      index++
      continue
    }
    const runStart = index
    while (value[index] === '`') index++
    if (index - runStart === length) return runStart
  }
  return -1
}

function normalizeStrongBoundary(value: string) {
  return value
    .replace(/(^|[^\p{L}\p{N}])\*\*([^*\n]*?[\p{P}\p{S}])\*\*(?=[\p{L}\p{N}])/gu, '$1**$2** ')
    .replace(/(^|[^\p{L}\p{N}])__([^_\n]*?[\p{P}\p{S}])__(?=[\p{L}\p{N}])/gu, '$1__$2__ ')
}
