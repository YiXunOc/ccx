<template>
  <v-alert v-if="!logicalChannelUid" type="warning" variant="tonal" density="compact" class="mb-2">
    {{ t('channelEditor.protocolModels.missingLogicalUid') }}
  </v-alert>
  <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-2">{{ error }}</v-alert>
  <v-alert v-if="saved" type="success" variant="tonal" density="compact" class="mb-2">
    {{ t('channelEditor.protocolModels.selectionSaved') }}
  </v-alert>
  <ProtocolModelAvailability
    :key="logicalChannelUid || 'unidentified'"
    :routes="routes" :loading="loading || reading"
    :preferences="preferences" :editable="ready && !!logicalChannelUid"
    :saving-preferences="saving" @save-preferences="save" @refreshed="emit('refreshed')"
  />
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import api from '../../services/api'
import type { ChannelProtocolRoute, ProtocolModelPreferences } from '../../services/api-types'
import { useI18n } from '../../i18n'
import ProtocolModelAvailability from './ProtocolModelAvailability.vue'
const props = defineProps<{ logicalChannelUid?: string; routes: ChannelProtocolRoute[]; loading?: boolean }>()
const emit = defineEmits<{ refreshed: [] }>()
const { t } = useI18n()
const preferences = ref<ProtocolModelPreferences>({})
const reading = ref(false)
const saving = ref(false)
const ready = ref(false)
const saved = ref(false)
const error = ref('')
let generation = 0
watch(() => props.logicalChannelUid, async uid => {
  const current = ++generation
  ready.value = false
  saved.value = false
  error.value = ''
  preferences.value = {}
  reading.value = false
  saving.value = false
  if (!uid) return
  reading.value = true
  try {
    const channel = await api.getLogicalChannel(uid)
    if (current !== generation) return
    preferences.value = channel.protocolModelPreferences ?? {}
    ready.value = true
  } catch (err) {
    if (current === generation) error.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (current === generation) reading.value = false
  }
}, { immediate: true })
async function save(next: ProtocolModelPreferences) {
  const uid = props.logicalChannelUid
  if (!uid || !ready.value || saving.value) return
  const current = generation
  // Empty protocol lists are removed so clearing the last selection sends an explicit {}.
  const map = Object.fromEntries(Object.entries(next).filter(([, models]) => models.length)) as ProtocolModelPreferences
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    await api.updateLogicalChannel(uid, { common: { protocolModelPreferences: map } })
    if (current !== generation) return
    preferences.value = map
    saved.value = true
  } catch (err) {
    if (current === generation) error.value = err instanceof Error ? err.message : t('channelEditor.protocolModels.selectionSaveFailed')
  } finally {
    if (current === generation) saving.value = false
  }
}
</script>
