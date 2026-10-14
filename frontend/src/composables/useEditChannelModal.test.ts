// @vitest-environment jsdom
import { defineComponent, nextTick, type PropType } from 'vue'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel } from '../services/api'
import { runChannelSaveTransaction, type ChannelSaveProgress } from './channelSaveTransaction'
import { useEditChannelModal } from './useEditChannelModal'

const { disableGroupModel } = vi.hoisted(() => ({
  disableGroupModel: vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 }),
}))

vi.mock('../services/api', () => ({
  ApiService: class {
    disableGroupModel = disableGroupModel
  },
}))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('../stores/channel', () => ({ useChannelStore: () => ({ refreshChannels: vi.fn(), currentChannelsData: { channels: [] } }) }))
vi.mock('../stores/dialog', () => ({ useDialogStore: () => ({ editingChannel: null }) }))

const channel = {
  index: 3,
  name: 'test-channel',
  serviceType: 'claude',
  baseUrl: 'https://example.invalid',
  apiKeys: [],
} as Channel

function mountEditor() {
  let state!: ReturnType<typeof useEditChannelModal>
  const Host = defineComponent({
    props: {
      show: { type: Boolean, required: true },
      channel: { type: Object as PropType<Channel>, required: true },
      channelType: { type: String as PropType<'messages'>, required: true },
    },
    setup(props, { emit }) {
      state = useEditChannelModal(props, emit)
      return () => null
    },
  })
  const wrapper = mount(Host, { props: { show: false, channel, channelType: 'messages' } })
  return { wrapper, state }
}

describe('useEditChannelModal staged group model disables', () => {
  beforeEach(() => {
    disableGroupModel.mockReset()
    disableGroupModel.mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
  })

  it('discards staging without disabling a model on cancel and direct close', async () => {
    const { wrapper, state } = mountEditor()
    try {
      await wrapper.setProps({ show: true })
      expect(state.isEditing.value).toBe(true)
      state.stageGroupModelDisable('test-key', 'model-x')
      expect(state.pendingGroupModelDisables.value).toEqual([{ key: 'test-key', model: 'model-x' }])

      state.handleCancel()
      expect(wrapper.emitted('update:show')).toEqual([[false]])
      expect(state.pendingGroupModelDisables.value).toEqual([])
      await wrapper.setProps({ show: false })
      await nextTick()
      expect(disableGroupModel).not.toHaveBeenCalled()

      await wrapper.setProps({ show: true })
      expect(state.isEditing.value).toBe(true)
      expect(state.pendingGroupModelDisables.value).toEqual([])
      state.stageGroupModelDisable('test-key', 'model-x')
      expect(state.pendingGroupModelDisables.value).toHaveLength(1)
      await wrapper.setProps({ show: false })
      await nextTick()
      expect(state.pendingGroupModelDisables.value).toEqual([])
      expect(disableGroupModel).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
    }
  })

  it('flushes staged disables once through the emitted save callback after saving', async () => {
    const { wrapper, state } = mountEditor()
    try {
      await wrapper.setProps({ show: true })
      state.stageGroupModelDisable('test-key', 'model-x')
      state.formRef.value = { validate: vi.fn().mockResolvedValue({ valid: true }) }
      await state.handleSubmit()
      const flush = wrapper.emitted('save')?.[0]?.[3] as (() => Promise<void>) | undefined
      expect(flush).toBeTypeOf('function')
      const save = vi.fn().mockResolvedValue({ success: true })
      const close = vi.fn()
      const refresh = vi.fn().mockResolvedValue(undefined)
      await runChannelSaveTransaction({ save, flush, close, refresh })
      expect(save).toHaveBeenCalledTimes(1)
      expect(disableGroupModel).toHaveBeenCalledTimes(1)
      expect(disableGroupModel).toHaveBeenCalledWith('messages', 3, 'test-key', 'model-x')
      expect(state.pendingGroupModelDisables.value).toEqual([])
      expect(close).toHaveBeenCalledTimes(1)
      expect(refresh).toHaveBeenCalledTimes(1)
    } finally {
      wrapper.unmount()
    }
  })

  it('真实排除失败不通知成功或关窗，重试不重复保存主渠道，重开隔离状态', async () => {
    const { wrapper, state } = mountEditor()
    try {
      await wrapper.setProps({ show: true })
      state.stageGroupModelDisable('test-key', 'model-x')
      state.formRef.value = { validate: vi.fn().mockResolvedValue({ valid: true }) }
      const save = vi.fn().mockResolvedValue({ success: true })
      const close = vi.fn()
      const onSaved = vi.fn()
      const refresh = vi.fn().mockResolvedValue(undefined)
      const submit = async () => {
        await state.handleSubmit()
        const events = wrapper.emitted('save')!
        const event = events[events.length - 1]!
        const progress = (event[1] as { progress: ChannelSaveProgress }).progress
        const onComplete = event[2] as () => void
        const flush = event[3] as () => Promise<void>
        try { return await runChannelSaveTransaction({ save, progress, payload: event[0], flush, close, onSaved, refresh }) }
        finally { onComplete() }
      }
      disableGroupModel.mockRejectedValueOnce(new Error('failed'))
      await expect(submit()).rejects.toThrow('渠道已保存，但模型排除部分失败')
      expect(state.pendingGroupModelDisables.value).toHaveLength(1)
      expect(state.submitting.value).toBe(false)
      expect(onSaved).not.toHaveBeenCalled()
      expect(close).not.toHaveBeenCalled()
      await submit()
      expect(save).toHaveBeenCalledTimes(1)
      expect(close).toHaveBeenCalledTimes(1)
      expect(onSaved).toHaveBeenCalledTimes(1)
      await wrapper.setProps({ show: false })
      await wrapper.setProps({ show: true })
      state.stageGroupModelDisable('test-key', 'model-x')
      await submit()
      expect(save).toHaveBeenCalledTimes(2)
      expect(disableGroupModel).toHaveBeenCalledTimes(3)
    } finally { wrapper.unmount() }
  })

  it('does not flush or close when saving fails', async () => {
    const { wrapper, state } = mountEditor()
    try {
      await wrapper.setProps({ show: true })
      state.stageGroupModelDisable('test-key', 'model-x')
      state.formRef.value = { validate: vi.fn().mockResolvedValue({ valid: true }) }
      await state.handleSubmit()
      const flush = wrapper.emitted('save')?.[0]?.[3] as (() => Promise<void>) | undefined
      expect(flush).toBeTypeOf('function')
      const close = vi.fn()
      const refresh = vi.fn()
      await expect(runChannelSaveTransaction({
        save: async () => { throw new Error('save failed') },
        flush,
        close,
        refresh,
      })).rejects.toThrow('save failed')
      expect(disableGroupModel).not.toHaveBeenCalled()
      expect(state.pendingGroupModelDisables.value).toEqual([{ key: 'test-key', model: 'model-x' }])
      expect(close).not.toHaveBeenCalled()
      expect(refresh).not.toHaveBeenCalled()
      expect(wrapper.props('show')).toBe(true)
    } finally {
      wrapper.unmount()
    }
  })
})
