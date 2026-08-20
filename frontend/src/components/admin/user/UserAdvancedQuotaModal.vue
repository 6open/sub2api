<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.advancedQuota.title')"
    width="normal"
    @close="$emit('close')"
  >
    <div v-if="user" class="space-y-5">
      <p class="text-sm text-gray-600 dark:text-gray-400">
        {{ user.email }}
      </p>

      <div v-if="user.role === 'admin'" class="rounded-lg border border-primary-200 bg-primary-50 p-4 text-sm text-primary-700 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-300">
        {{ t('admin.users.advancedQuota.adminUnlimited') }}
      </div>
      <div v-else-if="loading" class="py-8 text-center text-sm text-gray-500">
        {{ t('common.loading') }}
      </div>
      <template v-else>
        <div class="grid grid-cols-3 gap-3">
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <p class="text-xs text-gray-500">{{ t('admin.users.advancedQuota.used') }}</p>
            <p class="mt-1 font-semibold text-gray-900 dark:text-white">${{ fmt(usage) }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <p class="text-xs text-gray-500">{{ t('admin.users.advancedQuota.remaining') }}</p>
            <p class="mt-1 font-semibold text-primary-600 dark:text-primary-400">${{ fmt(remaining) }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <p class="text-xs text-gray-500">{{ t('admin.users.advancedQuota.weeklyLimit') }}</p>
            <p class="mt-1 font-semibold text-gray-900 dark:text-white">${{ fmt(currentLimit) }}</p>
          </div>
        </div>

        <label class="block">
          <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.users.advancedQuota.newWeeklyLimit') }}
          </span>
          <input v-model.number="limitInput" type="number" min="0" step="1" class="input w-full" />
        </label>

        <div class="flex items-center justify-between border-t border-gray-200 pt-4 dark:border-dark-700">
          <button type="button" class="btn-secondary" :disabled="resetting" @click="resetUsage">
            <Icon name="refresh" size="sm" />
            {{ resetting ? t('common.loading') : t('admin.users.advancedQuota.resetUsage') }}
          </button>
          <div class="flex gap-2">
            <button type="button" class="btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
            <button type="button" class="btn-primary" :disabled="saving || !validLimit" @click="saveLimit">
              {{ saving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminUser } from '@/types'
import type { PlatformQuotaItem } from '@/api/admin/users'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits<{ close: []; success: [] }>()
const { t } = useI18n()
const appStore = useAppStore()

const quota = ref<PlatformQuotaItem | null>(null)
const limitInput = ref(0)
const loading = ref(false)
const saving = ref(false)
const resetting = ref(false)
const usage = computed(() => quota.value?.weekly_usage_usd ?? 0)
const currentLimit = computed(() => quota.value?.weekly_limit_usd ?? 0)
const remaining = computed(() => Math.max(0, currentLimit.value - usage.value))
const validLimit = computed(() => Number.isFinite(limitInput.value) && limitInput.value >= 0)

const fmt = (value: number) => Number.isFinite(value) ? value.toFixed(2) : '0.00'

async function load() {
  if (!props.user || props.user.role === 'admin') return
  loading.value = true
  try {
    const response = await adminAPI.users.getPlatformQuotas(props.user.id)
    quota.value = response.platform_quotas.find((item) => item.platform === 'openai_advanced') ?? null
    limitInput.value = quota.value?.weekly_limit_usd ?? 0
  } catch (error) {
    appStore.showError(t('admin.users.advancedQuota.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function saveLimit() {
  if (!props.user || !validLimit.value) return
  saving.value = true
  try {
    quota.value = await adminAPI.users.updateOpenAIAdvancedQuota(props.user.id, limitInput.value)
    appStore.showSuccess(t('admin.users.advancedQuota.saved'))
    emit('success')
  } catch (error) {
    appStore.showError(t('admin.users.advancedQuota.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function resetUsage() {
  if (!props.user) return
  resetting.value = true
  try {
    const response = await adminAPI.users.resetPlatformQuotaWindow(props.user.id, 'openai_advanced', 'weekly')
    quota.value = response.platform_quotas.find((item) => item.platform === 'openai_advanced') ?? null
    appStore.showSuccess(t('admin.users.advancedQuota.resetSuccess'))
    emit('success')
  } catch (error) {
    appStore.showError(t('admin.users.advancedQuota.resetFailed'))
  } finally {
    resetting.value = false
  }
}

watch(() => [props.show, props.user?.id], ([show]) => {
  if (show) void load()
}, { immediate: true })
</script>
