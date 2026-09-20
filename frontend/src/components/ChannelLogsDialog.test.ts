// @vitest-environment jsdom
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import ChannelLogsDialog from './ChannelLogsDialog.vue'

const mocks = vi.hoisted(() => ({ getChannelLogs: vi.fn() }))
vi.mock('../services/api', () => ({ api: mocks }))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../composables/useGlobalTick', () => ({ useGlobalTick: () => ({ onTick: vi.fn(), start: vi.fn(), stop: vi.fn() }) }))
const slot = defineComponent({ template: '<div><slot /></div>' })

describe('ChannelLogsDialog usage', () => {
  it.each([
    { usage: { totalTokens: 170, inputTokens: 150, outputTokens: 20, cacheReadTokens: 40 }, values: ['170', '150', '20', '40'] },
    { usage: { totalTokens: 0, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0 }, values: ['0', '0', '0', '0'] },
    { usage: undefined, values: ['—', '—', '—', '—'] },
  ])('shows protocol and token counts without hiding zero or inventing missing usage', async ({ usage, values }) => {
    mocks.getChannelLogs.mockResolvedValue({ logs: [{ requestId: 'one', timestamp: '2026-01-01', model: 'test', status: 'completed', requestKind: 'Chat', interfaceType: 'Responses', usage }] })
    const stubs = Object.fromEntries(['VDialog', 'VCard', 'VCardTitle', 'VCardText', 'VBtnToggle', 'VBtn', 'VList', 'VListItem', 'VListItemTitle', 'VChip', 'VExpandTransition'].map(name => [name, slot]))
    const wrapper = mount(ChannelLogsDialog, { props: { modelValue: false, channelIndex: 0, channelName: 'test', channelType: 'messages' }, global: { stubs: { ...stubs, VTooltip: true, VProgressCircular: true, VAlert: true, VIcon: true, VDivider: true, AutopilotTraceDetailDialog: true } } })
    await wrapper.setProps({ modelValue: true })
    await flushPromises()
    expect(wrapper.text()).toContain('Chat → Responses')
    for (const [i, key] of ['total', 'input', 'output', 'cacheRead'].entries()) {
      expect(wrapper.text()).toContain('channelLogs.tokens.' + key + ' ' + values[i])
    }
    wrapper.unmount()
  })
})
