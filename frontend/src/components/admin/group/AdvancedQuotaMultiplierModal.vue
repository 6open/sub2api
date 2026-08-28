<template>
  <BaseDialog
    :show="show"
    :title="t('admin.groups.advancedQuotaMultiplier.title')"
    width="narrow"
    @close="handleClose"
  >
    <form id="advanced-quota-multiplier-form" class="space-y-4" @submit.prevent="save">
      <div>
        <label
          for="advanced-quota-multiplier"
          class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300"
        >
          {{ t('admin.groups.advancedQuotaMultiplier.label') }}
        </label>
        <div class="relative">
          <input
            id="advanced-quota-multiplier"
            v-model="multiplierInput"
            class="input pr-9"
            data-testid="advanced-quota-multiplier-input"
            inputmode="decimal"
            min="0"
            required
            step="0.01"
            type="number"
          />
          <span
            class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400"
          >x</span>
        </div>
        <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.groups.advancedQuotaMultiplier.hint') }}
        </p>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="advanced-quota-multiplier-form"
          class="btn btn-primary"
          data-testid="advanced-quota-multiplier-save"
          :disabled="loading || saving"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import {
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
  useStepUp
} from '@/composables/useStepUp'

const props = defineProps<{
  show: boolean
  currentMultiplier: number | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'saved', value: number): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const loading = ref(false)
const saving = ref(false)
const multiplierInput = ref('0.2')

const setInputValue = (value: number | null | undefined) => {
  multiplierInput.value = String(value ?? 0.2)
}

const load = async () => {
  loading.value = true
  setInputValue(props.currentMultiplier)
  try {
    const settings = await adminAPI.settings.getSettings()
    setInputValue(settings.openai_advanced_quota_usage_multiplier)
  } catch (error) {
    appStore.showError(t('admin.groups.advancedQuotaMultiplier.loadFailed'))
    console.error('Failed to load advanced quota multiplier:', error)
  } finally {
    loading.value = false
  }
}

const save = async () => {
  if (loading.value || saving.value) return

  const value = Number(multiplierInput.value)
  if (!Number.isFinite(value) || value < 0) {
    appStore.showError(t('admin.groups.advancedQuotaMultiplier.invalid'))
    return
  }

  saving.value = true
  try {
    await stepUp.run(() => adminAPI.settings.updateSettings({
      openai_advanced_quota_usage_multiplier: value
    }))
    appStore.showSuccess(t('admin.groups.advancedQuotaMultiplier.saved', { value }))
    emit('saved', value)
    emit('close')
  } catch (error: unknown) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled')
      )
      return
    }
    appStore.showError(t('admin.groups.advancedQuotaMultiplier.saveFailed'))
    console.error('Failed to save advanced quota multiplier:', error)
  } finally {
    saving.value = false
  }
}

const handleClose = () => {
  if (!saving.value) emit('close')
}

watch(
  () => props.show,
  (shown) => {
    if (shown) void load()
  }
)
</script>
