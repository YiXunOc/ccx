// @vitest-environment jsdom
import { defineComponent, nextTick } from 'vue'
import { shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EditChannelModal from './EditChannelModal.vue'

const mocks = vi.hoisted(() => ({ finalize: vi.fn(), submit: vi.fn() }))
vi.mock('../services/api', () => ({
  ApiError: class extends Error {},
  ApiService: class { async getChannelModels() { return { data: [] } } },
}))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('../stores/channel', () => ({ useChannelStore: () => ({ refreshChannels: vi.fn(), currentChannelsData: { channels: [] } }) }))
vi.mock('../stores/dialog', () => ({ useDialogStore: () => ({ editingChannel: null }) }))
vi.mock('../composables/useEditChannelModal', async importOriginal => {
  const original = await importOriginal<typeof import('../composables/useEditChannelModal')>()
  return {
    ...original,
    useEditChannelModal: (...args: Parameters<typeof original.useEditChannelModal>) => ({
      ...original.useEditChannelModal(...args), handleSubmit: mocks.submit,
    }),
  }
})
const section = defineComponent({
  setup(_, { expose }) { expose({ finalizeModelExclusions: mocks.finalize }) },
  template: '<div />',
})
const passthrough = defineComponent({ template: '<div><slot /></div>' })
const button = defineComponent({
  props: ['disabled'], emits: ['click'],
  template: `<button :disabled="disabled" @click="$emit('click')"><slot /></button>`,
})

describe('EditChannelModal model exclusions save gate', () => {
  beforeEach(() => {
    mocks.finalize.mockReset()
    mocks.submit.mockReset()
    HTMLElement.prototype.scrollIntoView = vi.fn()
  })
  it.each([false, true])('主保存先定稿，失败不调用保存：%s', async valid => {
    mocks.finalize.mockReturnValue(valid)
    const wrapper = shallowMount(EditChannelModal, {
      props: {
        show: false, channelType: 'messages',
        channel: { index: 3, name: 'test', serviceType: 'claude', baseUrl: 'https://example.invalid', apiKeys: ['test-key'] },
      },
      global: { stubs: {
        ApiKeyManagementSection: section,
        VDialog: passthrough, VCard: passthrough, VCardText: passthrough,
        VCardActions: passthrough, VForm: passthrough, VBtn: button,
        VIcon: true, VTextField: true, VSwitch: true, VCol: passthrough, VRow: passthrough,
        VSelect: true, VSpacer: true, VTooltip: true, VAlert: passthrough,
      } },
    })
    await wrapper.setProps({ show: true })
    await nextTick()
    const save = wrapper.findAll('button').find(item => item.text().includes('app.actions.save'))!
    expect(save).toBeDefined()
    await save.trigger('click')
    expect(mocks.finalize).toHaveBeenCalledTimes(1)
    expect(mocks.submit).toHaveBeenCalledTimes(valid ? 1 : 0)
    if (valid) expect(mocks.finalize.mock.invocationCallOrder[0]).toBeLessThan(mocks.submit.mock.invocationCallOrder[0]!)
    wrapper.unmount()
  })
})
