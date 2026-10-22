import { describe, expect, it } from 'vitest'
import { parseModelMapping, formatModelMapping } from './modelMapping'

describe('physical channel model mapping', () => {
  it('round trips exact, case-sensitive names without changing route semantics', () => {
    const mapping = parseModelMapping('Model-A => Upstream-B\nmodel-a => upstream-b')
    expect(mapping).toEqual({ 'Model-A': 'Upstream-B', 'model-a': 'upstream-b' })
    expect(parseModelMapping(formatModelMapping(mapping))).toEqual(mapping)
  })

  it('rejects wildcard, duplicate, and malformed mappings', () => {
    expect(() => parseModelMapping('gpt-* => gpt-4')).toThrow()
    expect(() => parseModelMapping('A => B\nA => C')).toThrow()
    expect(() => parseModelMapping('A B => C')).toThrow()
    expect(() => parseModelMapping('A => B C')).toThrow()
    expect(() => parseModelMapping('A => B\tC')).toThrow()
    expect(() => parseModelMapping('A => B\nC')).toThrow()
    expect(() => parseModelMapping('A => ')).toThrow()
  })
})
