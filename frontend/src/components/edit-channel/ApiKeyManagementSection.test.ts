// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
// The bundled entry provides the real components without Node-incompatible CSS imports.
import { createVuetify, components } from 'vuetify/dist/vuetify.js'
import { computed, defineComponent, nextTick } from 'vue'
import { ApiService } from '../../services/api'
import type { Channel, LlmChannelDashboardResponse } from '../../services/api-types'
import { buildUnifiedChannelsData, type LlmChannelKind } from '../../utils/unifiedChannels'
import { mergeChannelsWithLocalData } from '../../utils/channelMerge'
import { useDisabledApiKeys } from '../../composables/useDisabledApiKeys'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ApiKeyManagementSection from './ApiKeyManagementSection.vue'
import { maskApiKey } from '../../utils/apiKeyMask'
import { groupModelPolicyKey } from '../../utils/channelApiKeys'
import type { APIKeyConfig } from '../../services/api-types'

vi.hoisted(() => {
  // jsdom does not expose CSS.supports required by Vuetify's browser feature checks.
  vi.stubGlobal('CSS', { supports: () => false })
})

const apiMocks = vi.hoisted(() => ({
  patchKeyMultiplier: vi.fn(),
  getManagedAccounts: vi.fn(),
  setKimiConsoleToken: vi.fn(),
  sha256KeyHash: vi.fn(),
}))

vi.mock('../../services/api', () => ({
  api: apiMocks,
  ApiError: class extends Error {},
  ApiService: class {
    async patchKeyMultiplier(...args: unknown[]) {
      return apiMocks.patchKeyMultiplier(...args)
    }

    async getManagedAccounts(...args: unknown[]) {
      return apiMocks.getManagedAccounts(...args)
    }

    async setKimiConsoleToken(...args: unknown[]) {
      return apiMocks.setKimiConsoleToken(...args)
    }
  },
}))

vi.mock('../../utils/hash', () => ({
  sha256KeyHash: (...args: unknown[]) => apiMocks.sha256KeyHash(...args),
}))

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) =>
    params?.count === undefined ? key : `${key}:${params.count}` }),
}))

const passthroughStub = defineComponent({ template: '<div v-bind="$attrs"><slot /></div>' })
const inputStub = defineComponent({
  props: ['modelValue', 'type', 'placeholder'],
  emits: ['update:modelValue'],
  template: '<input :type="type" :placeholder="placeholder" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
})
const selectStub = defineComponent({
  props: ['modelValue', 'items', 'itemTitle', 'itemValue', 'label'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
})
const buttonStub = defineComponent({
  props: ['disabled', 'variant', 'color'],
  emits: ['click'],
  template: '<button :disabled="disabled" v-bind="$attrs" @click="$emit(\'click\')" type="button"><slot /></button>',
})
const listItemStub = defineComponent({
  emits: ['click'],
  template: '<div v-bind="$attrs" @click="$emit(\'click\')"><slot name="prepend" /><slot /><slot name="append" /></div>',
})
const { VChip } = components as unknown as { VChip: typeof import('vuetify/components/VChip').VChip }
const chipStub = defineComponent({
  props: ['closable', 'disabled', 'closeLabel'],
  emits: ['click:close'],
  template: '<div v-bind="$attrs"><slot /><button v-if="closable !== undefined && closable !== false" class="chip-close" :aria-label="closeLabel" :disabled="disabled" @click="$emit(\'click:close\')" type="button">x</button></div>',
})
const comboboxStub = defineComponent({
  props: ['modelValue', 'items', 'label'],
  emits: ['update:modelValue'],
  template: '<div><input class="combobox-stub-input" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)" /></div>',
})

const mountSection = (props: Record<string, unknown> = {}, realChips = false) => mount(ApiKeyManagementSection, {
  props: {
    apiKeys: ['sk-1'],
    disabledKeys: [],
    apiKeyConfigs: [],
    keyModelsStatus: new Map(),
    isEditing: true,
    restoringKey: '',
    dialogOpen: true,
    providerId: '',
    accountUid: '',
    ...props,
  },
  global: {
    plugins: realChips ? [createVuetify({ components: { VChip } })] : [],
    stubs: {
      VCard: passthroughStub,
      VCardTitle: passthroughStub,
      VCardText: passthroughStub,
      VCardActions: passthroughStub,
      VIcon: passthroughStub,
      VChip: realChips ? false : chipStub,
      VTooltip: defineComponent({
        // 渲染 activator slot（scope 提供 props 空对象），使 tooltip 内按钮可被定位
        template: '<div><slot name="activator" :props="{}" /><slot /></div>',
      }),
      VProgressCircular: passthroughStub,
      VProgressLinear: passthroughStub,
      VAlert: passthroughStub,
      VForm: passthroughStub,
      VRow: passthroughStub,
      VCol: passthroughStub,
      VSpacer: passthroughStub,
      VList: passthroughStub,
      VListItem: listItemStub,
      VListItemTitle: passthroughStub,
      VListItemSubtitle: passthroughStub,
      VDialog: defineComponent({
        props: ['modelValue'],
        emits: ['update:modelValue'],
        template: '<div v-if="modelValue"><slot /></div>',
      }),
      VSelect: selectStub,
      VTextField: inputStub,
      VBtn: buttonStub,
      VExpandTransition: passthroughStub,
      VDivider: passthroughStub,
      VCombobox: comboboxStub,
      UsageQuotaRows: passthroughStub,
    },
  },
})

describe('ApiKeyManagementSection', () => {
  it('Dashboard物理规则经实际合并进入编辑Key标签，跨协议去重、Key隔离并重开', async () => {
    const rule = (key: string, model: string, quotaGroup = '') => ({ key, model, quotaGroup, disabledAt: '2026-09-18T00:00:00Z' })
    const physical = (index: number, rules: ReturnType<typeof rule>[]): Channel => ({
      index, name: 'dashboard-channel', logicalChannelUid: 'logical-1', channelUid: 'ch-' + index,
      serviceType: 'openai', baseUrl: 'https://example.invalid', apiKeys: ['sk-1', 'sk-2', 'sk-3', 'sk-4'],
      apiKeyConfigs: ['sk-1', 'sk-2', 'sk-3', 'sk-4'].map((key, i) => ({ key, keyUid: 'uid-' + i, quotaGroup: i < 2 ? '' : 'coding' })),
      disabledGroupModels: rules,
    })
    const stats = { multiChannelMode: true, activeChannelCount: 2, traceAffinityCount: 0, traceAffinityTTL: '0s', failureThreshold: 3, windowSize: 10 }
    const dashboard: LlmChannelDashboardResponse = JSON.parse(JSON.stringify({ dashboards: {
      messages: { channels: [physical(3, [rule('sk-1', 'model-a'), rule('sk-2', 'other-key'), rule('sk-3', 'group-a', 'coding')])], metrics: [], stats },
      chat: { channels: [physical(7, [rule('sk-1', 'model-a'), rule('sk-1', 'model-b'), rule('sk-4', 'group-b', 'coding')])], metrics: [], stats },
      responses: { channels: [], metrics: [], stats }, gemini: { channels: [], metrics: [], stats },
    } }))
    const loadEditor = () => {
      const kinds: LlmChannelKind[] = ['messages', 'chat', 'responses', 'gemini']
      const data = Object.fromEntries(kinds.map(kind => [kind, { channels: mergeChannelsWithLocalData(dashboard.dashboards[kind].channels, []), current: -1 }])) as Parameters<typeof buildUnifiedChannelsData>[0]
      const logical = buildUnifiedChannelsData(data).channels[0]!
      expect(logical.protocolRoutes).toHaveLength(2)
      expect(logical.disabledGroupModels).toHaveLength(5)
      const state = useDisabledApiKeys({ apiService: new ApiService(), channel: computed(() => logical), channelType: computed(() => 'messages'), form: { apiKeys: logical.apiKeys, apiKeyConfigs: logical.apiKeyConfigs }, emitError: vi.fn() })
      return mountSection({ apiKeys: logical.apiKeys, apiKeyConfigs: logical.apiKeyConfigs, channelUid: logical.channelUid,
        channelKind: logical.routeKind, disabledGroupModels: state.visibleDisabledGroupModels.value })
    }
    for (let opening = 0; opening < 2; opening++) {
      const wrapper = loadEditor()
      try {
        for (const [key, expected, absent] of [
          ['sk-1', ['model-a', 'model-b'], ['other-key', 'group-a', 'group-b']],
          ['sk-2', ['other-key'], ['model-a', 'model-b', 'group-a']],
          ['sk-3', ['group-a', 'group-b'], ['model-a', 'other-key']],
          ['sk-4', ['group-a', 'group-b'], ['model-b', 'other-key']],
        ] as const) {
          await wrapper.find('[data-key-row="' + key + '"] [aria-label="channelCard.keyDetail"]').trigger('click')
          await nextTick()
          const detail = wrapper.find('[data-key-row="' + key + '"] .volcengine-key-detail')
          for (const model of expected) expect(detail.text()).toContain(model)
          for (const model of absent) expect(detail.text()).not.toContain(model)
        }
      } finally { wrapper.unmount() }
    }
  })
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.patchKeyMultiplier.mockResolvedValue({
      keyUid: 'uid-1',
      group: '',
      groupMultiplier: 0,
      maxMultiplier: 0,
      consumptionPolicy: 'opportunistic',
      effectiveCostClass: 'zero',
      status: 'manual',
      reason: 'ok',
      eligible: true,
      updatedAt: '2026-08-11T00:00:00Z',
    })
    apiMocks.getManagedAccounts.mockResolvedValue({ accounts: [] })
    apiMocks.setKimiConsoleToken.mockResolvedValue({
      usage: { validatedAt: '2026-08-13T00:00:00Z' },
    })
    apiMocks.sha256KeyHash.mockResolvedValue('hash-stub')
  })

  it('renders opportunistic chip on key row', async () => {
    const wrapper = mountSection({
      apiKeys: ['sk-1'],
      disabledKeys: [],
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', consumptionPolicy: 'opportunistic', effectiveCostClass: 'zero', groupMultiplier: 0, maxGroupMultiplier: 0 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await vi.waitFor(() => expect(wrapper.text()).toContain('subscription.keyMultiplier.policyChip'))
    expect(wrapper.text()).toContain('zero')
  })

  it('does not repeat manual in the multiplier chip', async () => {
    const wrapper = mountSection({
      apiKeys: ['sk-1'],
      disabledKeys: [],
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2, multiplierSource: 'manual', multiplierSyncStatus: 'manual' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()

    expect(wrapper.text().toLowerCase().match(/manual/g)).toHaveLength(1)
  })

  it('opens multiplier editor with current policy', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2, consumptionPolicy: 'normal' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('[aria-label="channelCard.keyDetail"]').trigger('click')
    await nextTick()

    const select = wrapper.findComponent(selectStub)
    expect(select.exists()).toBe(true)
    expect(select.props('modelValue')).toBe('normal')
  })

  it('saves multiplier with consumption policy on policy change (no save button)', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('[aria-label="channelCard.keyDetail"]').trigger('click')
    await nextTick()

    // 消耗策略选择即暂存进表单 apiKeyConfigs（随渠道主保存），不再即时 PATCH
    const select = wrapper.findComponent(selectStub)
    await select.find('select').setValue('opportunistic')

    const events = wrapper.emitted('update:apiKeyConfigs')
    expect(events).toBeTruthy()
    const lastConfig = events![events!.length - 1][0] as Array<Record<string, unknown>>
    expect(lastConfig[0]).toMatchObject({ key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, consumptionPolicy: 'opportunistic' })
    expect(apiMocks.patchKeyMultiplier).not.toHaveBeenCalled()
  })

  it('stages decimal multiplier inputs as JSON numbers while typing', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('[aria-label="channelCard.keyDetail"]').trigger('click')
    await nextTick()

    const multiplierInputs = wrapper.findAllComponents(inputStub)
      .filter(input => input.props('type') === 'number')
    // 倍率上限已统一为渠道级：Key 倍率编辑只剩分组倍率一个数字输入。
    expect(multiplierInputs).toHaveLength(1)
    await multiplierInputs[0].vm.$emit('update:modelValue', '0.15')
    await nextTick()

    const events = wrapper.emitted('update:apiKeyConfigs')
    expect(events).toBeTruthy()
    const lastConfig = events![events!.length - 1][0] as Array<Record<string, unknown>>
    expect(lastConfig[0].groupMultiplier).toBe(0.15)
    expect(lastConfig[0].groupMultiplier).toBeTypeOf('number')
    expect(apiMocks.patchKeyMultiplier).not.toHaveBeenCalled()
  })

  it('flushes the focused multiplier input before the save shortcut runs', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 0.12 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    await wrapper.find('[aria-label="channelCard.keyDetail"]').trigger('click')
    await nextTick()

    const multiplierInput = wrapper.findAllComponents(inputStub)
      .find(input => input.props('type') === 'number')
    expect(multiplierInput).toBeDefined()
    const inputElement = multiplierInput!.element as HTMLInputElement
    inputElement.value = '0.13'
    await multiplierInput!.trigger('keydown.enter', { ctrlKey: true })

    const events = wrapper.emitted('update:apiKeyConfigs') || []
    const lastConfig = events[events.length - 1]?.[0] as Array<Record<string, unknown>>
    expect(lastConfig[0].groupMultiplier).toBe(0.13)
  })

  it('expands multiplier editor inline without save/cancel/mark-public buttons', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, maxGroupMultiplier: 2 },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()
    // 倍率设置从弹窗改为行下展开：点击倍率按钮出现内联面板
    const detailBtn = wrapper.find('[aria-label="channelCard.keyDetail"]')
    expect(detailBtn.exists()).toBe(true)
    await detailBtn.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('subscription.keyMultiplier.policy')
    expect(wrapper.text()).toContain('subscription.keyMultiplier.value')
    // 变更即保存：无保存/取消/标记公开按钮，展示自动保存提示
    const actionButtons = wrapper.findAllComponents(buttonStub)
      .filter(b => ['app.actions.save', 'app.actions.cancel', 'subscription.keyMultiplier.markPublic'].some(k => b.text().includes(k)))
    expect(actionButtons).toHaveLength(0)
    expect(wrapper.text()).toContain('subscription.keyMultiplier.stagedHint')
  })

  it('reopens another key with the staged multiplier after the parent updates props', async () => {
    const wrapper = mountSection({
      apiKeys: ['sk-1', 'sk-2'],
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1 },
        { key: 'sk-2', keyUid: 'uid-2' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()

    const detailButtons = wrapper.findAll('[aria-label="channelCard.keyDetail"]')
    await detailButtons[1].trigger('click')
    await nextTick()

    const multiplierInput = wrapper.findAllComponents(inputStub)
      .find(input => input.props('type') === 'number')
    expect(multiplierInput).toBeDefined()
    await multiplierInput!.vm.$emit('update:modelValue', '0.12')
    await multiplierInput!.vm.$emit('change', '0.12')
    await nextTick()

    const configEvents = wrapper.emitted('update:apiKeyConfigs') || []
    const staged = configEvents[configEvents.length - 1]?.[0] as APIKeyConfig[]
    expect(staged?.find(config => config.key === 'sk-2')?.groupMultiplier).toBe(0.12)
    await wrapper.setProps({ apiKeyConfigs: staged })
    await nextTick()

    const stagedDetailButton = wrapper.findAll('[aria-label="channelCard.keyDetail"]')[1]
    await stagedDetailButton.trigger('click')
    await nextTick()
    await wrapper.findAll('[aria-label="channelCard.keyDetail"]')[1].trigger('click')
    await nextTick()
    const reopenedInput = wrapper.findAllComponents(inputStub)
      .find(input => input.props('type') === 'number')
    expect(reopenedInput?.props('modelValue')).toBe(0.12)
  })

  it('keeps Kimi credential bound to the correct key row after save and reload with reversed credential order', async () => {
    const alphaKey = 'sk-alpha-1234567890'
    const betaKey = 'sk-beta-0987654321'
    const alphaMask = maskApiKey(alphaKey)
    const betaMask = maskApiKey(betaKey)

    apiMocks.getManagedAccounts.mockResolvedValue({
      accounts: [
        {
          accountUid: 'acct-kimi',
          providerId: 'kimi',
          name: 'kimi',
          credentials: [
            {
              credentialUid: 'cred-alpha',
              keyMask: alphaMask,
              hasKimiConsoleToken: false,
            },
            {
              credentialUid: 'cred-beta',
              keyMask: betaMask,
              hasKimiConsoleToken: false,
            },
          ],
          channels: [],
          endpointCount: 0,
        },
      ],
    })

    apiMocks.setKimiConsoleToken.mockImplementation(async (_accountUid: string, credentialUid: string) => {
      apiMocks.getManagedAccounts.mockResolvedValue({
        accounts: [
          {
            accountUid: 'acct-kimi',
            providerId: 'kimi',
            name: 'kimi',
            credentials: [
              {
                credentialUid: 'cred-beta',
                keyMask: betaMask,
                hasKimiConsoleToken: false,
              },
              {
                credentialUid: 'cred-alpha',
                keyMask: alphaMask,
                hasKimiConsoleToken: credentialUid === 'cred-alpha',
                kimiCodeUsage: credentialUid === 'cred-alpha'
                  ? {
                      weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
                      totalQuota: { used: 12, limit: 100, remaining: 88 },
                      rateLimits: [],
                      validatedAt: '2026-08-13T00:00:00Z',
                    }
                  : undefined,
              },
            ],
            channels: [],
            endpointCount: 0,
          },
        ],
      })
      return {
        usage: {
          weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
          totalQuota: { used: 12, limit: 100, remaining: 88 },
          rateLimits: [],
          validatedAt: '2026-08-13T00:00:00Z',
        },
      }
    })

    const wrapper = mountSection({
      apiKeys: [alphaKey, betaKey],
      providerId: 'kimi',
      accountUid: 'acct-kimi',
      serviceType: 'claude',
    })

    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(wrapper.html()).toContain('kimiConsoleToken.notConfigured'))

    const alphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    const betaRow = wrapper.get(`[data-key-row="${betaKey}"]`)

    await alphaRow.get('button[aria-label="kimiConsoleToken.title"]').trigger('click')
    await nextTick()

    const expandedAlphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    expect(expandedAlphaRow.html()).toContain('kimiConsoleToken.notConfigured')
    expect(betaRow.html()).not.toContain('kimiConsoleToken.configured')

    const tokenInput = expandedAlphaRow.get('input[type="password"]')
    await tokenInput.setValue('kimi-token-alpha')

    const saveButton = expandedAlphaRow.findAll('button')
      .find(button => button.text().includes('kimiConsoleToken.verifyAndSave'))
    expect(saveButton).toBeTruthy()
    await saveButton!.trigger('click')

    await vi.waitFor(() => expect(apiMocks.setKimiConsoleToken).toHaveBeenCalledWith(
      'acct-kimi',
      'cred-alpha',
      'kimi-token-alpha',
    ))

    apiMocks.getManagedAccounts.mockResolvedValueOnce({
      accounts: [
        {
          accountUid: 'acct-kimi',
          providerId: 'kimi',
          name: 'kimi',
          credentials: [
            {
              credentialUid: 'cred-beta',
              keyMask: betaMask,
              hasKimiConsoleToken: false,
            },
            {
              credentialUid: 'cred-alpha',
              keyMask: alphaMask,
              hasKimiConsoleToken: true,
              kimiCodeUsage: {
                weeklyUsage: { used: 12, limit: 100, remaining: 88, resetTime: '2026-08-20T00:00:00Z' },
                totalQuota: { used: 12, limit: 100, remaining: 88 },
                rateLimits: [],
                validatedAt: '2026-08-13T00:00:00Z',
              },
            },
          ],
          channels: [],
          endpointCount: 0,
        },
      ],
    })

    await wrapper.setProps({ accountUid: 'acct-kimi-reload' })
    await wrapper.setProps({ accountUid: 'acct-kimi' })
    await vi.waitFor(() => expect(apiMocks.getManagedAccounts).toHaveBeenCalledTimes(3))
    await nextTick()

    const reloadedAlphaRow = wrapper.get(`[data-key-row="${alphaKey}"]`)
    const reloadedBetaRow = wrapper.get(`[data-key-row="${betaKey}"]`)

    expect(reloadedAlphaRow.html()).toContain('kimiConsoleToken.configured')
    expect(reloadedAlphaRow.html()).toContain('kimiConsoleToken.validatedAt')
    expect(reloadedBetaRow.html()).not.toContain('kimiConsoleToken.configured')
  })
})

describe('分组模型排除行内化', () => {
  const openInput = async () => {
    const wrapper = mountSection({
      channelUid: 'ch-1', channelKind: 'messages',
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1' }],
    })
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    return { wrapper, input: wrapper.get('input.model-exclusion-text') }
  }
  it('分号批量过滤空项与大小写重复，输入期间不定稿', async () => {
    const { wrapper, input } = await openInput()
    await input.setValue(' Model-A ; model-a ； ; prefix* ; *suffix ; *contains* ')
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    await input.trigger('blur')
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([
      ['sk-1', 'Model-A'], ['sk-1', 'prefix*'], ['sk-1', '*suffix'], ['sk-1', '*contains*'],
    ])
    expect((input.element as HTMLInputElement).value).toBe('')
  })
  it.each(['a*b', '**', 'a**', '*a*b*', 'a?', '[a]', '!a', '^a'])('非法语法保留整批文本且主保存入口返回失败：%s', async invalid => {
    const { wrapper, input } = await openInput()
    await input.setValue('valid; ' + invalid)
    expect(wrapper.vm.finalizeModelExclusions()).toBe(false)
    await nextTick()
    expect(wrapper.get('[role="alert"]').text()).toContain('channelCard.groupModelInvalidPattern')
    expect((input.element as HTMLInputElement).value).toBe('valid; ' + invalid)
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    await input.setValue('corrected*')
    expect(wrapper.vm.finalizeModelExclusions()).toBe(true)
    await nextTick()
    expect(wrapper.find('#model-exclusion-error').exists()).toBe(false)
  })
  it('暂存标签框内回显，删除仅发本地撤销事件', async () => {
    const { wrapper } = await openInput()
    await wrapper.setProps({ pendingGroupModelDisables: [{ key: 'sk-1', model: 'draft*' }] })
    const field = wrapper.get('.model-exclusion-input')
    expect(field.text()).toContain('draft*')
    await field.get('button.chip-close').trigger('click')
    expect(wrapper.emitted('unstage-group-model-disable')).toEqual([['sk-1', 'draft*']])
    expect(wrapper.emitted('restore-group-model')).toBeUndefined()
    await wrapper.setProps({ pendingGroupModelDisables: [] })
    expect(field.text()).not.toContain('draft*')
  })
  it('Ctrl+Enter交由主保存定稿，不重复触发全模型确认', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { wrapper, input } = await openInput()
    await input.setValue('*')
    await input.trigger('keydown.enter', { ctrlKey: true })
    expect(confirm).not.toHaveBeenCalled()
    expect(wrapper.vm.finalizeModelExclusions()).toBe(true)
    expect(confirm).toHaveBeenCalledTimes(1)
    confirm.mockRestore()
  })
  it('主保存无需失焦即可定稿，重复调用不会重复发事件', async () => {
    const { wrapper, input } = await openInput()
    await input.setValue('new-a；new-b')
    expect(wrapper.vm.finalizeModelExclusions()).toBe(true)
    expect(wrapper.vm.finalizeModelExclusions()).toBe(true)
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([['sk-1', 'new-a'], ['sk-1', 'new-b']])
  })
  it('IME组合期间Enter、blur和主保存均不定稿', async () => {
    const { wrapper, input } = await openInput()
    await input.trigger('compositionstart')
    await input.setValue('model-x')
    await input.trigger('keydown.enter', { isComposing: true })
    await input.trigger('blur')
    expect(wrapper.vm.finalizeModelExclusions()).toBe(false)
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    await input.trigger('compositionend')
    await input.trigger('keydown.enter')
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([['sk-1', 'model-x']])
  })
  it('移焦到主保存或取消不提前确认全模型，主保存独立校验', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { wrapper, input } = await openInput()
    await input.setValue('*')
    const save = document.createElement('button')
    save.dataset.modelExclusionAction = 'save'
    await input.trigger('blur', { relatedTarget: save })
    expect(confirm).not.toHaveBeenCalled()
    expect(wrapper.vm.finalizeModelExclusions()).toBe(false)
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    save.dataset.modelExclusionAction = 'cancel'
    await input.trigger('blur', { relatedTarget: save })
    expect(confirm).toHaveBeenCalledTimes(1)
    confirm.mockRestore()
  })
  it('全模型排除确认取消保留文本，接受后才暂存', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { wrapper, input } = await openInput()
    await input.setValue('model-a;*')
    expect(wrapper.vm.finalizeModelExclusions()).toBe(false)
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    expect((input.element as HTMLInputElement).value).toBe('model-a;*')
    confirm.mockReturnValue(true)
    expect(wrapper.vm.finalizeModelExclusions()).toBe(true)
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([['sk-1', 'model-a'], ['sk-1', '*']])
    confirm.mockRestore()
  })
  it('取消弹窗丢弃未定稿文本且不发送恢复请求', async () => {
    const { wrapper, input } = await openInput()
    await input.setValue('new-a')
    await wrapper.setProps({ dialogOpen: false })
    await wrapper.setProps({ dialogOpen: true })
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    expect((wrapper.get('input.model-exclusion-text').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    expect(wrapper.emitted('restore-group-model')).toBeUndefined()
  })

  it('实际 VChip 关闭后保留已保存标签，直到父级恢复成功移除记录', async () => {
    const saved = { quotaGroup: '', key: 'sk-1', model: 'model-x', disabledAt: '' }
    const wrapper = mountSection({
      channelUid: 'ch-1', channelKind: 'messages',
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1' }],
      disabledGroupModels: [saved],
    }, true)
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    const chip = wrapper.findAllComponents(VChip).find(component => component.props('closable'))!
    await chip.get('.v-chip__close').trigger('click')
    expect(wrapper.emitted('restore-group-model')).toEqual([[saved]])
    expect(chip.isVisible()).toBe(true)
    await wrapper.setProps({ changingGroupModel: groupModelPolicyKey(saved) })
    expect(chip.isVisible()).toBe(true)
    // 接口失败仅清除忙碌标志，不移除策略记录。
    await wrapper.setProps({ changingGroupModel: '' })
    expect(chip.isVisible()).toBe(true)
    expect(chip.find('.v-chip__close').exists()).toBe(true)
    await wrapper.setProps({ disabledGroupModels: [] })
    expect(wrapper.findAllComponents(VChip).filter(component => component.props('closable'))).toHaveLength(0)
  })
  it.each(['g1', ''])('已保存与暂存模型选择不重复发事件，恢复后可重加：%s', async group => {
    const saved = { quotaGroup: group ? ' g1 ' : ' ', key: 'sk-1', model: ' Model-X ', disabledAt: '' }
    const wrapper = mountSection({
      apiKeys: ['sk-1', 'sk-2'],
      channelUid: 'ch-1', channelKind: 'messages',
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1', quotaGroup: group }, { key: 'sk-2', keyUid: 'uid-2', quotaGroup: group }],
      disabledGroupModels: [saved],
      pendingGroupModelDisables: [{ key: group ? 'sk-2' : 'sk-1', model: 'Pending-X' }],
    })
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    const combo = wrapper.get('input.model-exclusion-text')
    await combo.setValue(' MODEL-X ; pending-x ')
    await combo.trigger('blur')
    await nextTick()
    expect(wrapper.emitted('stage-group-model-disable')).toBeUndefined()
    await wrapper.setProps({ disabledGroupModels: [], pendingGroupModelDisables: [] })
    await combo.setValue('model-x')
    await combo.trigger('keydown.enter')
    await nextTick()
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([['sk-1', 'model-x']])
  })

  it('独立 Key 不受其他 Key 已保存或暂存的同模型策略阻断', async () => {
    const wrapper = mountSection({
      apiKeys: ['sk-1', 'sk-2'],
      channelUid: 'ch-1', channelKind: 'messages',
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1' }, { key: 'sk-2', keyUid: 'uid-2' }],
      disabledGroupModels: [{ quotaGroup: '', key: 'sk-2', model: 'model-x', disabledAt: '' }],
      pendingGroupModelDisables: [{ key: 'sk-2', model: 'model-x' }],
    })
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    await wrapper.get('input.model-exclusion-text').setValue('MODEL-X')
    await wrapper.get('input.model-exclusion-text').trigger('blur')
    expect(wrapper.emitted('stage-group-model-disable')).toEqual([['sk-1', 'MODEL-X']])
  })

  it.each(['g1', ''])('已保存标签可关闭，发原始记录且忙碌时不可撤销：%s', async group => {
    const saved = { quotaGroup: group, key: group ? 'sk-2' : 'sk-1', model: 'model-x', disabledAt: '' }
    const wrapper = mountSection({
      channelUid: 'ch-1', channelKind: 'messages',
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1', quotaGroup: group }],
      disabledGroupModels: [saved],
    })
    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    const chip = wrapper.findAllComponents(chipStub).find(component => component.props('closeLabel') === 'channelCard.restoreGroupModel')!
    const close = chip.get('button.chip-close')
    expect(close.attributes('aria-label')).toBe('channelCard.restoreGroupModel')
    expect(close.attributes('disabled')).toBeUndefined()
    expect(chip.attributes('title')).toBe('channelCard.restoreGroupModel')
    await close.trigger('click')
    expect(wrapper.emitted('restore-group-model')).toEqual([[saved]])
    await wrapper.setProps({ changingGroupModel: groupModelPolicyKey(saved) })
    expect(close.attributes()).toHaveProperty('disabled')
    expect(chip.find('[indeterminate]').exists()).toBe(true)
    await close.trigger('click')
    chip.vm.$emit('click:close') // 即使收到关闭事件，忙碌守卫仍拦截。
    await wrapper.setProps({ changingGroupModel: 'another-policy' })
    expect(close.attributes()).toHaveProperty('disabled')
    await close.trigger('click')
    chip.vm.$emit('click:close')
    expect(wrapper.emitted('restore-group-model')).toEqual([[saved]])
  })
  it.each([
    { group: undefined, secondMultiplier: 0.12 },
    { group: '', secondMultiplier: 0.08 },
    { group: '   ', secondMultiplier: 0.12 },
  ])('未分组 Key 独立显示作用范围：$group / $secondMultiplier', async ({ group, secondMultiplier }) => {
    const wrapper = mountSection({
      apiKeys: ['sk-1', 'sk-2'],
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', quotaGroup: group, groupMultiplier: 0.08 },
        { key: 'sk-2', keyUid: 'uid-2', quotaGroup: group, groupMultiplier: secondMultiplier },
      ],
      channelUid: 'ch-1',
      channelKind: 'chat',
    })

    for (const button of wrapper.findAll('[aria-label="channelCard.keyDetail"]')) {
      await button.trigger('click')
      expect(wrapper.text()).toContain('channelCard.independentKey')
      expect(wrapper.text()).toContain('channelCard.affectedCurrentKey')
      expect(wrapper.text()).toContain('channelCard.keyModelPolicy')
      expect(wrapper.text()).not.toContain('channelCard.affectedGroupKeys')
    }
  })

  it('明确分组按最新配置统计，排除其他分组与未分组 Key', async () => {
    const configs = [
      { key: 'sk-1', keyUid: 'uid-1', quotaGroup: ' coding ' },
      { key: 'sk-2', keyUid: 'uid-2', quotaGroup: 'coding', enabled: false },
      { key: 'sk-3', keyUid: 'uid-3', quotaGroup: 'other' },
      { key: 'sk-4', keyUid: 'uid-4' },
    ]
    const wrapper = mountSection({
      apiKeys: configs.map(config => config.key),
      apiKeyConfigs: configs,
      channelUid: 'ch-1',
      channelKind: 'chat',
    })

    await wrapper.get('[aria-label="channelCard.keyDetail"]').trigger('click')
    expect(wrapper.text()).toContain('channelCard.affectedGroupKeys:2')
    expect(wrapper.text()).toContain('channelCard.groupModelPolicy')
    await wrapper.setProps({ apiKeyConfigs: configs.map(config => ({ ...config, quotaGroup: '' })) })
    expect(wrapper.text()).toContain('channelCard.affectedCurrentKey')
    expect(wrapper.text()).not.toContain('channelCard.affectedGroupKeys')
  })

  it('同模型的独立 Key 排除记录可区分并正确恢复', async () => {
    const records = ['sk-alpha-123456', 'sk-beta-654321'].map(key => ({
      quotaGroup: '', key, model: 'model-x', disabledAt: '2026-09-25T00:00:00Z',
    }))
    const wrapper = mountSection({ disabledGroupModels: records })
    const rows = wrapper.findAll('.group-model-policy-list .px-3')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain(maskApiKey(records[0].key))
    expect(rows[1].text()).toContain(maskApiKey(records[1].key))
    await rows[1].get('button').trigger('click')
    expect(wrapper.emitted('restore-group-model')?.[0]).toEqual([records[1]])

    await wrapper.setProps({ disabledGroupModels: [records[1]] })
    expect(wrapper.get('.group-model-policy-list').text()).toContain(maskApiKey(records[1].key))
    expect(wrapper.get('.group-model-policy-list').text()).not.toContain(maskApiKey(records[0].key))
  })

  it('统一详情按钮行内展开，模型选定即提交且无对话框按钮', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', quotaGroup: 'g1' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()

    const tuneBtn = wrapper.find('[aria-label="channelCard.keyDetail"]')
    expect(tuneBtn.exists()).toBe(true)
    await tuneBtn.trigger('click')
    await nextTick()

    // 行内面板展开：上下文 caption + 自动提交提示；无取消/禁用对话框按钮
    expect(wrapper.text()).toContain('channelCard.affectedGroupKeys')
    expect(wrapper.text()).toContain('channelCard.groupModelInlineHint')
    const dialogButtons = wrapper.findAllComponents(buttonStub)
      .filter(b => b.text().includes('channelCard.disableGroupModel') || b.text().includes('app.actions.cancel'))
    expect(dialogButtons).toHaveLength(0)

    // 模型定稿（combobox change）即暂存排除事件，面板保持展开（随主保存提交）
    const comboInput = wrapper.find('input.model-exclusion-text')
    expect(comboInput.exists()).toBe(true)
    await comboInput.setValue('kimi-k3')
    await comboInput.trigger('blur')
    await nextTick()

    const events = wrapper.emitted('stage-group-model-disable')
    expect(events).toBeTruthy()
    expect(events![0]).toEqual(['sk-1', 'kimi-k3'])
    // 不再即时提交（旧 disable-group-model 事件随暂存化移除），面板保持展开
    expect(wrapper.emitted('disable-group-model')).toBeFalsy()
    expect(wrapper.text()).toContain('channelCard.groupModelInlineHint')
  })

  it('同一 Key 可连续暂存多个模型并回显已保存策略', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1', quotaGroup: 'g1' }],
      channelUid: 'ch-1',
      channelKind: 'messages',
      disabledGroupModels: [
        { quotaGroup: 'g1', model: 'saved-a', disabledAt: '2026-09-18T00:00:00Z' },
        { quotaGroup: 'g1', model: 'saved-b', disabledAt: '2026-09-18T01:00:00Z' },
      ],
    })
    await wrapper.find('[aria-label="channelCard.keyDetail"]').trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('saved-a')
    expect(wrapper.text()).toContain('saved-b')

    const comboInput = wrapper.find('input.model-exclusion-text')
    await comboInput.setValue('new-a')
    await comboInput.trigger('blur')
    await comboInput.setValue('new-b')
    await comboInput.trigger('blur')
    await nextTick()

    expect(wrapper.emitted('stage-group-model-disable')).toEqual([
      ['sk-1', 'new-a'],
      ['sk-1', 'new-b'],
    ])
  })
  it('再次点击统一详情按钮收起面板（toggle）', async () => {
    const wrapper = mountSection({
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1' },
      ],
      channelUid: 'ch-1',
      channelKind: 'messages',
    })
    await nextTick()

    const tuneBtn = wrapper.find('[aria-label="channelCard.keyDetail"]')
    await tuneBtn.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('channelCard.groupModelInlineHint')

    await tuneBtn.trigger('click')
    await nextTick()
    expect(wrapper.text()).not.toContain('channelCard.groupModelInlineHint')
    expect(wrapper.emitted('stage-group-model-disable')).toBeFalsy()
  })
})
