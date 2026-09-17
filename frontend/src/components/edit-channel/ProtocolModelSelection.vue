<template>
  <div class="protocol-model-selection mt-3">
    <v-select
      v-model="selected"
      :items="options"
      :label="t('channelEditor.protocolModels.selectionLabel')"
      :hint="t('channelEditor.protocolModels.selectionHint')"
      :disabled="saving"
      multiple chips closable-chips clearable persistent-hint
      variant="outlined" density="compact"
    />
    <div v-if="!selected.length" class="text-caption text-medium-emphasis mt-2">
      {{ t('channelEditor.protocolModels.automaticSelection') }}
    </div>
    <v-alert v-if="missing.length" type="warning" variant="tonal" density="compact" class="mt-2">
      {{ t('channelEditor.protocolModels.missingSelection') }}: {{ missing.join(', ') }}
    </v-alert>
    <v-alert v-if="selectedConflicts.length" type="warning" variant="tonal" density="compact" class="mt-2">
      {{ t('channelEditor.protocolModels.conflictSelection') }}: {{ selectedConflicts.join(', ') }}
    </v-alert>
    <div class="d-flex ga-2 mt-2">
      <v-btn data-action="save-models" size="small" color="primary" variant="tonal" :loading="saving" :disabled="saving || !dirty || selectedConflicts.length > 0" @click="emit('save', [...selected])">
        {{ t('channelEditor.protocolModels.saveSelection') }}
      </v-btn>
      <v-btn data-action="clear-models" size="small" variant="text" :disabled="saving || !selected.length" @click="selected = []">
        {{ t('channelEditor.protocolModels.clearSelection') }}
      </v-btn>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '../../i18n'

const props = withDefaults(defineProps<{
  models?: string[]
  discoveredModels: string[]
  conflicts?: string[]
  saving?: boolean
}>(), { models: () => [], conflicts: () => [], saving: false })
const emit = defineEmits<{ save: [models: string[]] }>()
const { t } = useI18n()
const draft = ref<string[]>([])
const selected = computed({
  get: () => draft.value,
  set: (value: string[] | null) => { draft.value = value ?? [] },
})
// Discovery refreshes may replace arrays without changing persisted configuration.
watch(() => JSON.stringify(props.models), () => { draft.value = [...props.models] }, { immediate: true })
const options = computed(() => [...new Set([...props.discoveredModels, ...props.models])].sort((a, b) => a.localeCompare(b)))
const selectedConflicts = computed(() => selected.value.filter(model => props.conflicts.includes(model)))
const missing = computed(() => selected.value.filter(model => !props.discoveredModels.includes(model)))
const dirty = computed(() => JSON.stringify([...selected.value].sort()) !== JSON.stringify([...props.models].sort()))
</script>
