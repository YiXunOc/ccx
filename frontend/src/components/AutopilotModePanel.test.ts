// @vitest-environment jsdom
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AutopilotModePanel from './AutopilotModePanel.vue'
import type { SmartRoutingConfig } from '@/services/api-types'
import zhCN from '@/locales/zh-CN.json'
import en from '@/locales/en.json'
import id from '@/locales/id.json'

vi.mock('@/i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const VSwitchStub = defineComponent({
  name: 'VSwitch',
  props: {
    modelValue: { type: Boolean, required: true },
    disabled: { type: Boolean, default: false },
    label: { type: String, default: '' },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    return () => h('button', {
      class: 'kill-switch-control',
      disabled: props.disabled,
      'data-model': String(props.modelValue),
      onClick: () => emit('update:modelValue', !props.modelValue),
    }, props.label)
  },
})

const VSelectStub = defineComponent({
  name: 'VSelect',
  props: {
    disabled: { type: Boolean, default: false },
  },
  setup(props) {
    return () => h('select', {
      class: 'select-control',
      disabled: props.disabled,
    })
  },
})

const VBtnStub = defineComponent({
  name: 'VBtn',
  props: {
    color: { type: String, default: '' },
    disabled: { type: Boolean, default: false },
  },
  emits: ['click'],
  setup(props, { emit, slots }) {
    return () => h('button', {
      class: props.color === 'primary' ? 'save-button' : 'reset-button',
      disabled: props.disabled,
      onClick: () => emit('click'),
    }, slots.default?.())
  },
})

const passthroughStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

function config(overrides: Partial<SmartRoutingConfig> = {}): SmartRoutingConfig {
  return {
    killSwitchActive: false,
    killSwitchConfigured: false,
    killSwitchForced: false,
    costPreference: 'balanced',
    scenario: 'auto',
    ...overrides,
  }
}

function mountPanel(value: SmartRoutingConfig) {
  return mount(AutopilotModePanel, {
    props: { config: value, saving: false },
    global: {
      stubs: {
        VAlert: passthroughStub,
        VBtn: VBtnStub,
        VCard: passthroughStub,
        VCardText: passthroughStub,
        VCardTitle: passthroughStub,
        VIcon: passthroughStub,
        VSelect: VSelectStub,
        VSwitch: VSwitchStub,
      },
    },
  })
}

describe('AutopilotModePanel KillSwitch', () => {
  it('普通状态下可编辑 KillSwitch 并检测变更', async () => {
    const wrapper = mountPanel(config())
    const toggle = wrapper.get('.kill-switch-control')

    expect(toggle.attributes('disabled')).toBeUndefined()
    expect(wrapper.get('.save-button').attributes('disabled')).toBeDefined()

    await toggle.trigger('click')

    expect(wrapper.get('.save-button').attributes('disabled')).toBeUndefined()
    await wrapper.get('.save-button').trigger('click')
    expect(wrapper.emitted<SmartRoutingConfig[]>('update:config')?.[0]?.[0].killSwitchActive).toBe(true)
  })

  it('配置急停可关闭，并保留 configured/forced 响应状态', async () => {
    const wrapper = mountPanel(config({
      killSwitchActive: true,
      killSwitchConfigured: true,
    }))

    await wrapper.get('.kill-switch-control').trigger('click')
    await wrapper.get('.save-button').trigger('click')

    expect(wrapper.emitted<SmartRoutingConfig[]>('update:config')?.[0]?.[0]).toMatchObject({
      killSwitchActive: false,
      killSwitchConfigured: true,
      killSwitchForced: false,
    })
  })

  it('环境变量强制急停时禁用开关、显示来源并保持其他设置禁用', () => {
    const wrapper = mountPanel(config({
      killSwitchActive: true,
      killSwitchConfigured: false,
      killSwitchForced: true,
    }))

    expect(wrapper.get('.kill-switch-control').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('autopilot.modePanel.killSwitchForced')
    expect(wrapper.findAll('.select-control')).toHaveLength(2)
    expect(wrapper.findAll('.select-control').every(select => select.attributes('disabled') !== undefined)).toBe(true)
  })

  it('reset 恢复 props 中的 KillSwitch 值', async () => {
    const wrapper = mountPanel(config())

    await wrapper.get('.kill-switch-control').trigger('click')
    expect(wrapper.get('.kill-switch-control').attributes('data-model')).toBe('true')

    await wrapper.get('.reset-button').trigger('click')
    expect(wrapper.get('.kill-switch-control').attributes('data-model')).toBe('false')
    expect(wrapper.get('.save-button').attributes('disabled')).toBeDefined()
  })

  it('props 更新时同步 configured/forced 状态', async () => {
    const wrapper = mountPanel(config())

    await wrapper.setProps({
      config: config({
        killSwitchActive: true,
        killSwitchConfigured: false,
        killSwitchForced: true,
      }),
    })
    await nextTick()

    expect(wrapper.get('.kill-switch-control').attributes('data-model')).toBe('true')
    expect(wrapper.get('.kill-switch-control').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('autopilot.modePanel.killSwitchForced')
  })

  it('三种语言说明环境变量强制来源', () => {
    const key = 'autopilot.modePanel.killSwitchForced'
    expect(zhCN[key]).toContain('AUTOPILOT_KILL_SWITCH')
    expect(en[key]).toContain('AUTOPILOT_KILL_SWITCH')
    expect(id[key]).toContain('AUTOPILOT_KILL_SWITCH')
  })
})
