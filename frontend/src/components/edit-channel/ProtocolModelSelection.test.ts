// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import ProtocolModelSelection from './ProtocolModelSelection.vue'

vi.mock('../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const select = defineComponent({
  props: ['modelValue', 'items'], emits: ['update:modelValue'], template: '<div />',
})
const button = defineComponent({ props: ['disabled'], emits: ['click'], template: `<button :disabled="disabled" @click="$emit('click')"><slot /></button>` })
const mountEditor = (props = {}) => mount(ProtocolModelSelection, {
  props: { models: ['saved'], discoveredModels: ['all-a', 'all-b'], conflicts: [], saving: false, ...props },
  global: { stubs: { VSelect: select, VBtn: button, VAlert: { template: '<div><slot /></div>' } } },
})

describe('ProtocolModelSelection', () => {
  it('保留未发现配置并提供完整发现清单', () => {
    const wrapper = mountEditor()
    expect(wrapper.findComponent(select).props('items')).toEqual(['all-a', 'all-b', 'saved'])
    expect(wrapper.findComponent(select).props('modelValue')).toEqual(['saved'])
    expect(wrapper.text()).toContain('channelEditor.protocolModels.missingSelection')
    expect(wrapper.text()).toContain('saved')
  })
  it('多选后显式保存，不静默丢弃失效模型', async () => {
    const wrapper = mountEditor()
    wrapper.findComponent(select).vm.$emit('update:modelValue', ['all-a', 'saved'])
    await wrapper.vm.$nextTick()
    await wrapper.get('[data-action="save-models"]').trigger('click')
    expect(wrapper.emitted('save')).toEqual([[['all-a', 'saved']]])
  })
  it('清空后保存空数组恢复自动选择', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-action="clear-models"]').trigger('click')
    await wrapper.get('[data-action="save-models"]').trigger('click')
    expect(wrapper.emitted('save')).toEqual([[[]]])
    expect(wrapper.text()).toContain('channelEditor.protocolModels.automaticSelection')
  })
  it('显示冲突但不自行裁决，并在刷新后回显配置', async () => {
    const wrapper = mountEditor({ conflicts: ['saved'] })
    expect(wrapper.text()).toContain('channelEditor.protocolModels.conflictSelection')
    await wrapper.setProps({ models: ['all-b'] })
    expect(wrapper.findComponent(select).props('modelValue')).toEqual(['all-b'])
  })
  it('新选择冲突模型时提示并阻止保存，清除后可保存', async () => {
    const wrapper = mountEditor({ conflicts: ['all-a'] })
    wrapper.findComponent(select).vm.$emit('update:modelValue', ['all-a'])
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[data-action="save-models"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('channelEditor.protocolModels.conflictSelection')
    await wrapper.get('[data-action="clear-models"]').trigger('click')
    expect(wrapper.get('[data-action="save-models"]').attributes('disabled')).toBeUndefined()
  })
  it('后台发现刷新不覆盖尚未保存的编辑', async () => {
    const wrapper = mountEditor()
    wrapper.findComponent(select).vm.$emit('update:modelValue', ['all-a'])
    await wrapper.setProps({ models: ['saved'], discoveredModels: ['all-a', 'new'] })
    expect(wrapper.findComponent(select).props('modelValue')).toEqual(['all-a'])
  })
})
