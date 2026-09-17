// @vitest-environment jsdom
import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import LogicalProtocolModels from './LogicalProtocolModels.vue'
const api = vi.hoisted(() => ({ getLogicalChannel: vi.fn(), updateLogicalChannel: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))
vi.mock('../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const mountPanel = (logicalChannelUid?: string) => mount(LogicalProtocolModels, {
  props: { logicalChannelUid, routes: [] },
  global: { stubs: { ProtocolModelAvailability: true, VAlert: { template: '<div><slot /></div>' } } },
})
describe('LogicalProtocolModels', () => {
  it('通过逻辑实体读取并用单次逻辑PUT显式清空map', async () => {
    api.getLogicalChannel.mockResolvedValue({ protocolModelPreferences: { chat: ['a'] } })
    api.updateLogicalChannel.mockResolvedValue(undefined)
    const wrapper = mountPanel('logical-1')
    await flushPromises()
    const panel = wrapper.findComponent({ name: 'ProtocolModelAvailability' })
    expect(panel.props('preferences')).toEqual({ chat: ['a'] })
    panel.vm.$emit('savePreferences', { chat: [] })
    await flushPromises()
    expect(api.updateLogicalChannel).toHaveBeenCalledWith('logical-1', { common: { protocolModelPreferences: {} } })
    expect(panel.props('preferences')).toEqual({})
  })
  it('缺UID明确提示且禁止编辑、不按账号猜测', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    expect(wrapper.text()).toContain('channelEditor.protocolModels.missingLogicalUid')
    expect(wrapper.findComponent({ name: 'ProtocolModelAvailability' }).props('editable')).toBe(false)
  })
  it('保存失败保留原配置且不显示成功', async () => {
    api.getLogicalChannel.mockResolvedValue({ protocolModelPreferences: { chat: ['a'] } })
    api.updateLogicalChannel.mockRejectedValue(new Error('conflict'))
    const wrapper = mountPanel('logical-2')
    await flushPromises()
    const panel = wrapper.findComponent({ name: 'ProtocolModelAvailability' })
    panel.vm.$emit('savePreferences', { chat: ['b'] })
    await flushPromises()
    expect(panel.props('preferences')).toEqual({ chat: ['a'] })
    expect(wrapper.text()).toContain('conflict')
    expect(wrapper.text()).not.toContain('channelEditor.protocolModels.selectionSaved')
  })
})
