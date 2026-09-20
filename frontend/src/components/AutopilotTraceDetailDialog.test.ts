// @vitest-environment jsdom
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import AutopilotTraceDetailDialog from './AutopilotTraceDetailDialog.vue'

const mocks = vi.hoisted(() => ({ getAutopilotTraceDetail: vi.fn() }))
vi.mock('../services/api', () => ({ api: mocks }))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../utils/clipboard', () => ({ writeClipboardText: vi.fn() }))
const slot = defineComponent({ template: '<div><slot /></div>' })

describe('AutopilotTraceDetailDialog protocol visibility', () => {
  it('always shows the upstream protocol field even without endpoint attempts', async () => {
    mocks.getAutopilotTraceDetail.mockResolvedValue({ trace: {
      traceUid: 'trace-one', schemaVersion: 2, requestKind: 'chat', taskClass: 'supervisor',
      requestedModel: 'gpt-test', candidates: [], comparisonStatus: 'unavailable', endpointAttempts: [],
    } })
    const stubs = Object.fromEntries(['VDialog', 'VCard', 'VCardTitle', 'VCardText', 'VCardActions', 'VBtn', 'VProgressCircular', 'VAlert', 'VDivider', 'VRow', 'VCol', 'VChip', 'VTable', 'VIcon', 'VSpacer'].map(name => [name, slot]))
    const wrapper = mount(AutopilotTraceDetailDialog, { props: { modelValue: false, traceUid: 'trace-one', upstreamRequestKind: 'Responses' }, global: { stubs: { ...stubs, VTooltip: slot } } })
    await wrapper.setProps({ modelValue: true })
    await flushPromises()
    expect(wrapper.text()).toContain('Upstream Protocol')
    expect(wrapper.text()).toContain('Responses')
    wrapper.unmount()
  })
})
