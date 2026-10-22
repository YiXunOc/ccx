import { describe, expect, it } from 'vitest'
import en from '@/locales/en.json'
import zhCN from '@/locales/zh-CN.json'

const mappingKeys = [
  'channelEditor.modelMapping.label',
  'channelEditor.modelMapping.placeholder',
  'channelEditor.modelMapping.hint',
  'channelEditor.modelMapping.example',
  'channelEditor.modelMapping.invalid',
] as const

describe('channel model mapping i18n', () => {
  it.each([en, zhCN])('provides all mapping guidance strings', messages => {
    for (const key of mappingKeys) {
      expect(messages[key]).toEqual(expect.any(String))
      expect(messages[key].trim()).not.toBe('')
    }
  })
})
