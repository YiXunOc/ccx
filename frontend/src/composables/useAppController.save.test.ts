// @vitest-environment jsdom
import { computed, effectScope, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiService, type Channel } from '../services/api'
import { useDisabledApiKeys } from './useDisabledApiKeys'
import { useAppController } from './useAppController'
import type { ChannelSaveProgress } from './channelSaveTransaction'

const mocks = vi.hoisted(() => ({
  saveChannel: vi.fn(), refreshChannels: vi.fn(), showToast: vi.fn(),
  closeAddChannelModal: vi.fn(), closeEditChannelModal: vi.fn(),
  editingChannel: null as Channel | null,
}))
vi.mock('vue', async importOriginal => ({
  ...await importOriginal<typeof import('vue')>(),
  onMounted: vi.fn(), onUnmounted: vi.fn(),
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/channels/messages' }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({}) }))
vi.mock('../i18n', () => ({ useI18n: () => ({ locale: { value: 'zh-CN' }, t: (key: string, params?: { message?: string }) => params?.message ?? key, setLocale: vi.fn() }) }))
vi.mock('../stores/channel', () => ({ useChannelStore: () => ({ activeTab: 'messages', saveChannel: mocks.saveChannel, refreshChannels: mocks.refreshChannels }) }))
vi.mock('../stores/dialog', () => ({ useDialogStore: () => mocks }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: false }) }))
vi.mock('../stores/preferences', () => ({ usePreferencesStore: () => ({}) }))
vi.mock('../stores/system', () => ({ useSystemStore: () => ({}) }))
vi.mock('./useTheme', () => ({ useAppTheme: () => ({ init: vi.fn() }) }))
vi.mock('./useDialogHotkeys', () => ({ useDialogHotkeys: vi.fn() }))
vi.mock('./useToasts', () => ({ useToasts: () => ({ showToast: mocks.showToast }) }))

describe('actual save notification caller', () => {
  it.each([false, true])('排除失败后编辑主表单，使用已有或创建返回ID更新 (%s)', async created => {
    const scope = effectScope()
    mocks.editingChannel = created ? null : { index: 3, routeKind: 'messages', apiKeys: ['test-key'] } as Channel
    mocks.saveChannel.mockResolvedValue({ success: true, message: 'saved', channelId: 3 })
    const controller = scope.run(() => useAppController())!
    const progress: ChannelSaveProgress = {}
    const flush = vi.fn().mockRejectedValueOnce(new Error('failed')).mockResolvedValue(undefined)
    const payload = { name: 'before', baseUrl: 'https://before.invalid', apiKeys: ['before-key'] } as Omit<Channel, 'index' | 'latency' | 'status'>
    try {
      await controller.saveChannel(payload, { progress }, undefined, flush)
      Object.assign(payload, { name: 'after', baseUrl: 'https://after.invalid', apiKeys: ['after-key'] })
      await controller.saveChannel(payload, { progress }, undefined, flush)
      expect(mocks.saveChannel).toHaveBeenCalledTimes(2)
      expect(mocks.saveChannel.mock.calls[0]![0]).toEqual({ name: 'before', baseUrl: 'https://before.invalid', apiKeys: ['before-key'] })
      expect(mocks.saveChannel.mock.calls[0]![1]).toBe(created ? null : 3)
      expect(mocks.saveChannel.mock.calls[1]).toEqual([payload, 3, expect.objectContaining({ channelType: 'messages' })])
      expect(progress.saved?.payload).toEqual(payload)
      expect(flush).toHaveBeenCalledTimes(2)
      expect(mocks.closeEditChannelModal).toHaveBeenCalledTimes(1)
    } finally { scope.stop() }
  })

  it('创建未返回ID时阻止变更重试完成，保留编辑和原保存快照', async () => {
    const scope = effectScope()
    const controller = scope.run(() => useAppController())!
    const progress: ChannelSaveProgress = {}
    const flush = vi.fn().mockRejectedValue(new Error('failed'))
    const payload = { name: 'before' } as Omit<Channel, 'index' | 'latency' | 'status'>
    try {
      await controller.saveChannel(payload, { progress }, undefined, flush)
      payload.name = 'after'
      await controller.saveChannel(payload, { progress }, undefined, flush)
      expect(mocks.saveChannel).toHaveBeenCalledTimes(1)
      expect(flush).toHaveBeenCalledTimes(1)
      expect(progress.saved?.payload).toEqual({ name: 'before' })
      expect(payload.name).toBe('after')
      expect(mocks.showToast).toHaveBeenCalledWith(expect.stringContaining('无法安全保存后续编辑'), 'error')
      expect(mocks.closeEditChannelModal).not.toHaveBeenCalled()
      expect(mocks.closeAddChannelModal).not.toHaveBeenCalled()
    } finally { scope.stop() }
  })
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.editingChannel = null
    mocks.saveChannel.mockResolvedValue({ success: true, message: 'saved' })
    mocks.refreshChannels.mockResolvedValue(undefined)
  })

  it('编辑after时仍只重试失败协议路由且保留首次目标快照', async () => {
    const scope = effectScope()
    const channel = ref({ index: 3, apiKeys: ['test-key'], protocolRoutes: [
      { kind: 'messages', index: 3, apiKeys: ['test-key'] },
      { kind: 'chat', index: 7, apiKeys: ['test-key'] },
    ] } as Channel)
    mocks.editingChannel = channel.value
    const disable = vi.spyOn(ApiService.prototype, 'disableGroupModel').mockImplementation(async kind => {
      if (kind === 'chat' && disable.mock.calls.length === 2) throw new Error('failed')
      return { success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 }
    })
    try {
      const controller = scope.run(() => useAppController())!
      const state = useDisabledApiKeys({ apiService: new ApiService(), channel: computed(() => channel.value), channelType: computed(() => 'messages'), form: { apiKeys: ['test-key'] }, emitError: vi.fn() })
      state.stageGroupModelDisable('test-key', 'model-x')
      const progress: ChannelSaveProgress = {}
      await controller.saveChannel({ name: 'before' } as Channel, { progress }, undefined, state.flushStagedGroupModelDisables)
      channel.value = { ...channel.value, index: 99, protocolRoutes: [] }
      mocks.editingChannel = channel.value
      await controller.saveChannel({ name: 'after' } as Channel, { progress }, undefined, state.flushStagedGroupModelDisables)
      expect(mocks.saveChannel.mock.calls.map(call => [call[0].name, call[1]])).toEqual([['before', 3], ['after', 3]])
      expect(disable.mock.calls).toEqual([['messages', 3, 'test-key', 'model-x'], ['chat', 7, 'test-key', 'model-x'], ['chat', 7, 'test-key', 'model-x']])
      expect(state.pendingGroupModelDisables.value).toEqual([])
    } finally { disable.mockRestore(); scope.stop() }
  })

  it('真实组合式失败只提示部分失败，重试不重复创建主渠道', async () => {
    const scope = effectScope()
    const channel = ref({ index: 3, apiKeys: ['test-key'] } as Channel)
    const disable = vi.spyOn(ApiService.prototype, 'disableGroupModel')
      .mockRejectedValueOnce(new Error('failed'))
      .mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    try {
      const controller = scope.run(() => useAppController())!
      const state = useDisabledApiKeys({ apiService: new ApiService(), channel: computed(() => channel.value), channelType: computed(() => 'messages'), form: { apiKeys: ['test-key'] }, emitError: vi.fn() })
      state.stageGroupModelDisable('test-key', 'model-x')
      const progress: ChannelSaveProgress = {}
      const complete = vi.fn()
      const payload = { name: 'test' } as Omit<Channel, 'index' | 'latency' | 'status'>
      await controller.saveChannel(payload, { progress }, complete, state.flushStagedGroupModelDisables)
      expect(mocks.showToast).toHaveBeenCalledWith(expect.stringContaining('渠道已保存，但模型排除部分失败'), 'warning')
      expect(mocks.showToast.mock.calls.some(call => call[1] === 'success')).toBe(false)
      expect(mocks.closeEditChannelModal).not.toHaveBeenCalled()
      expect(mocks.closeAddChannelModal).not.toHaveBeenCalled()
      expect(complete).toHaveBeenCalledTimes(1)
      expect(state.pendingGroupModelDisables.value).toHaveLength(1)
      await controller.saveChannel(payload, { progress }, complete, state.flushStagedGroupModelDisables)
      expect(mocks.saveChannel).toHaveBeenCalledTimes(1)
      expect(mocks.showToast).toHaveBeenCalledWith('saved', 'success')
      expect(mocks.closeEditChannelModal).toHaveBeenCalledTimes(1)
      expect(mocks.closeAddChannelModal).toHaveBeenCalledTimes(1)
      expect(state.pendingGroupModelDisables.value).toEqual([])
      expect(complete).toHaveBeenCalledTimes(2)
    } finally { disable.mockRestore(); scope.stop() }
  })
})
