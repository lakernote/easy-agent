// 部分 CommonMark 实现无法正确识别模型常生成的 **标题：**正文 边界，
// 会把星号原样显示。这里只在成对的强调标记后补必要的分隔空格，避免把
// 相邻强调片段的结束符和开始符错误配对；代码围栏和行内代码保持原样。
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
  const opening = new Map<string, number>()
  const insertionPoints: number[] = []
  let index = 0
  while (index < value.length - 1) {
    const marker = value.slice(index, index + 2)
    if ((marker !== '**' && marker !== '__') || !isStandaloneDelimiter(value, index, marker)) {
      index++
      continue
    }
    const openingIndex = opening.get(marker)
    if (openingIndex === undefined) {
      opening.set(marker, index)
    } else {
      const previous = characterBefore(value, index)
      const next = characterAt(value, index + marker.length)
      if (index > openingIndex + marker.length && /[\p{P}\p{S}]/u.test(previous) && /[\p{L}\p{N}]/u.test(next)) {
        insertionPoints.push(index + marker.length)
      }
      opening.delete(marker)
    }
    index += marker.length
  }
  if (insertionPoints.length === 0) return value
  let result = ''
  let start = 0
  for (const point of insertionPoints) {
    result += `${value.slice(start, point)} `
    start = point
  }
  return result + value.slice(start)
}

function isStandaloneDelimiter(value: string, index: number, marker: string) {
  const character = marker[0]
  if (value[index - 1] === character || value[index + marker.length] === character) return false
  let backslashes = 0
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor--) backslashes++
  return backslashes % 2 === 0
}

function characterBefore(value: string, index: number) {
  if (index <= 0) return ''
  const trailing = value.charCodeAt(index - 1)
  if (trailing >= 0xDC00 && trailing <= 0xDFFF && index > 1) return value.slice(index - 2, index)
  return value[index - 1]
}

function characterAt(value: string, index: number) {
  const codePoint = value.codePointAt(index)
  return codePoint === undefined ? '' : String.fromCodePoint(codePoint)
}
