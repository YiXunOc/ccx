export function parseModelMapping(text: string): Record<string, string> {
  const result: Record<string, string> = {}
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    const match = /^(.+?)\s*=>\s*(.+)$/.exec(line)
    if (!match) throw new Error(`Invalid model mapping: ${line}`)
    const source = match[1].trim()
    const target = match[2].trim()
    if (!source || !target || /\s|\*/.test(source) || /\s|\*/.test(target) || source.includes('=>') || target.includes('=>') || Object.prototype.hasOwnProperty.call(result, source)) {
      throw new Error(`Invalid model mapping: ${line}`)
    }
    Object.defineProperty(result, source, { value: target, enumerable: true, configurable: true, writable: true })
  }
  return result
}

export function formatModelMapping(mapping: Record<string, string> = {}): string {
  return Object.entries(mapping).map(([source, target]) => `${source} => ${target}`).join('\n')
}
