// @vitest-environment jsdom
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import ChannelLogsDialog from './ChannelLogsDialog.vue'
const VChip = defineComponent({ props: { tag: { type: String, default: 'span' } }, template: '<component :is="tag"><slot /></component>' })

const mocks = vi.hoisted(() => ({ getChannelLogs: vi.fn() }))
vi.mock('../services/api', () => ({ api: mocks }))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../composables/useGlobalTick', () => ({ useGlobalTick: () => ({ onTick: vi.fn(), start: vi.fn(), stop: vi.fn() }) }))
const slot = defineComponent({ template: '<div><slot /></div>' })

async function mountSelectionLogs(logs: Record<string, unknown>[]) {
  mocks.getChannelLogs.mockResolvedValue({ logs: logs.map(log => ({ timestamp: '2026-01-01', model: 'test', status: 'completed', ...log })) })
  const stubs = Object.fromEntries(['VDialog', 'VCard', 'VCardTitle', 'VCardText', 'VBtnToggle', 'VBtn', 'VList', 'VListItem', 'VListItemTitle', 'VExpandTransition'].map(name => [name, slot]))
  const wrapper = mount(ChannelLogsDialog, { props: { modelValue: false, channelIndex: 0, channelName: 'test', channelType: 'messages' }, global: { components: { VChip }, stubs: { ...stubs, VTooltip: true, VProgressCircular: true, VAlert: true, VIcon: true, VDivider: true, AutopilotTraceDetailDialog: true } } })
  await wrapper.setProps({ modelValue: true })
  await flushPromises()
  return wrapper
}

describe('ChannelLogsDialog selection details', () => {
  it('toggles successful selection diagnostics without expanding its request group', async () => {
    const wrapper = await mountSelectionLogs([
      { requestId: 'one', requestCorrelationId: 'group', selectionReason: 'priority_order', selectionTraceSummary: 'selected=0:primary/priority_order' },
      { requestId: 'two', requestCorrelationId: 'group', selectionReason: 'fallback', selectionTraceSummary: 'selected=1:backup/fallback' },
    ])
    const chip = wrapper.find('button[title="priority_order"]')
    expect(wrapper.findAll('.log-item')).toHaveLength(1)
    expect(wrapper.find('.log-detail-info').exists()).toBe(false)
    await chip.trigger('click')
    expect(chip.attributes('aria-expanded')).toBe('true')
    expect(wrapper.find('.log-detail-info').text()).toContain('requestCorrelationId: group')
    expect(wrapper.find('.log-detail-info').text()).toContain('selected=')
    expect(wrapper.findAll('.log-item')).toHaveLength(1)
    await chip.trigger('click')
    expect(wrapper.find('.log-detail-info').exists()).toBe(false)
    await wrapper.find('.log-item').trigger('click')
    expect(wrapper.findAll('.log-item')).toHaveLength(3)
    wrapper.unmount()
  })

  it('opens the existing trace dialog with the upstream protocol', async () => {
    const wrapper = await mountSelectionLogs([{ requestId: 'one', selectionReason: 'priority_order', autopilotTraceUid: 'rt_selection', interfaceType: 'responses' }])
    await wrapper.find('button[title="priority_order"]').trigger('click')
    const dialog = wrapper.findComponent({ name: 'AutopilotTraceDetailDialog' })
    expect(dialog.props()).toMatchObject({ modelValue: true, traceUid: 'rt_selection', upstreamRequestKind: 'responses' })
    wrapper.unmount()
  })

  it('does not create an action when selection diagnostics are absent', async () => {
    const wrapper = await mountSelectionLogs([{ requestId: 'one', selectionReason: 'priority_order', selectionTraceSummary: '  ' }])
    expect(wrapper.text()).toContain('priority_order')
    expect(wrapper.find('button[title="priority_order"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('preserves error details and escapes selection diagnostics', async () => {
    const wrapper = await mountSelectionLogs([{ requestId: 'one', errorInfo: 'upstream failure', selectionTraceSummary: '<img src=x onerror=alert(1)>' }])
    await wrapper.find('.log-item').trigger('click')
    expect(wrapper.text()).toContain('upstream failure')
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.find('.log-detail-info img').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('ChannelLogsDialog usage', () => {
  it.each([
    { usage: { totalTokens: 170, inputTokens: 150, outputTokens: 20, cacheReadTokens: 40 }, values: ['170', '150', '20', '40'] },
    { usage: { totalTokens: 0, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0 }, values: ['0', '0', '0', '0'] },
    { usage: undefined, values: ['—', '—', '—', '—'] },
  ])('shows protocol and token counts without hiding zero or inventing missing usage', async ({ usage, values }) => {
    mocks.getChannelLogs.mockResolvedValue({ logs: [{ requestId: 'one', timestamp: '2026-01-01', model: 'test', status: 'completed', requestKind: 'Chat', interfaceType: 'Responses', usage }] })
    const stubs = Object.fromEntries(['VDialog', 'VCard', 'VCardTitle', 'VCardText', 'VBtnToggle', 'VBtn', 'VList', 'VListItem', 'VListItemTitle', 'VExpandTransition'].map(name => [name, slot]))
    const wrapper = mount(ChannelLogsDialog, { props: { modelValue: false, channelIndex: 0, channelName: 'test', channelType: 'messages' }, global: { components: { VChip }, stubs: { ...stubs, VTooltip: true, VProgressCircular: true, VAlert: true, VIcon: true, VDivider: true, AutopilotTraceDetailDialog: true } } })
    await wrapper.setProps({ modelValue: true })
    await flushPromises()
    expect(wrapper.text()).toContain('Chat → Responses')
    for (const [i, key] of ['total', 'input', 'output', 'cacheRead'].entries()) {
      expect(wrapper.text()).toContain('channelLogs.tokens.' + key + ' ' + values[i])
    }
    wrapper.unmount()
  })
})
