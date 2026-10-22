import { describe, expect, it } from 'vitest'
import { parseModelMapping, formatModelMapping } from './model-mapping'

describe('physical channel model mapping', () => {
  it('preserves exact case-sensitive model names', () => {
    const mapping = parseModelMapping('Model-A => Upstream-B\nmodel-a => upstream-b')
    expect(mapping).toEqual({ 'Model-A': 'Upstream-B', 'model-a': 'upstream-b' })
    expect(parseModelMapping(formatModelMapping(mapping))).toEqual(mapping)
  })
  it('rejects wildcard, duplicate and malformed entries', () => {
    expect(() => parseModelMapping('gpt-* => gpt-4')).toThrow()
    expect(() => parseModelMapping('A => B\nA => C')).toThrow()
    expect(() => parseModelMapping('A B => C')).toThrow()
    expect(() => parseModelMapping('A => B C')).toThrow()
    expect(() => parseModelMapping('A => B\tC')).toThrow()
    expect(() => parseModelMapping('A => B\nC')).toThrow()
    expect(() => parseModelMapping('A => ')).toThrow()
  })
})
