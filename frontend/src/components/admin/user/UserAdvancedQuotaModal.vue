<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.advancedQuota.title')"
    width="normal"
    @close="$emit('close')"
  >
    <div v-if="user" class="space-y-5">
      <div class="flex min-w-0 items-center gap-3">
        <div class="flex h-10 w-10 flex-none items-center justify-center rounded-full bg-primary-100 text-sm font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
          {{ user.email.charAt(0).toUpperCase() }}
        </div>
        <p class="min-w-0 truncate font-medium text-gray-900 dark:text-gray-100">
          {{ user.email }}
        </p>
      </div>

      <div v-if="user.role === 'admin'" class="rounded-lg border border-primary-200 bg-primary-50 p-4 text-sm text-primary-700 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-300">
        {{ t('admin.users.advancedQuota.adminUnlimited') }}
      </div>
      <div v-else-if="loading" class="py-8 text-center text-sm text-gray-500">
        {{ t('common.loading') }}
      </div>
      <template v-else>
        <div class="grid grid-cols-3 divide-x divide-gray-200 overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-900/30">
          <div class="min-w-0 px-3 py-3.5 text-center">
            <p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ t('admin.users.advancedQuota.used') }}</p>
            <p class="mt-1 text-base font-semibold text-gray-900 dark:text-white">${{ fmt(usage) }}</p>
          </div>
          <div class="min-w-0 px-3 py-3.5 text-center">
            <p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ t('admin.users.advancedQuota.remaining') }}</p>
            <p class="mt-1 text-base font-semibold text-primary-600 dark:text-primary-400">${{ fmt(remaining) }}</p>
          </div>
          <div class="min-w-0 px-3 py-3.5 text-center">
            <p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ t('admin.users.advancedQuota.weeklyLimit') }}</p>
            <p class="mt-1 text-base font-semibold text-gray-900 dark:text-white">${{ fmt(currentLimit) }}</p>
          </div>
        </div>

        <label class="block space-y-2">
          <span class="input-label mb-0">
            {{ t('admin.users.advancedQuota.newWeeklyLimit') }}
          </span>
          <div class="relative">
            <span class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 font-medium text-gray-500 dark:text-gray-400">$</span>
            <input v-model.number="limitInput" type="number" min="0" step="0.01" class="input pl-8 pr-20" />
            <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400 dark:text-gray-500">USD / {{ t('admin.users.advancedQuota.week') }}</span>
          </div>
        </label>
      </template>
    </div>

    <template #footer>
      <div class="flex w-full flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between">
        <button
          v-if="user?.role !== 'admin'"
          type="button"
          class="btn btn-secondary"
          :disabled="loading || resetting"
          @click="resetUsage"
        >
          <Icon name="refresh" size="sm" />
          {{ resetting ? t('common.loading') : t('admin.users.advancedQuota.resetUsage') }}
        </button>
        <span v-else></span>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="emit('close')">
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="user?.role !== 'admin'"
            type="button"
            class="btn btn-primary"
            :disabled="loading || saving || !validLimit"
            @click="saveLimit"
          >
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </div>
    </template>
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
    emit('close')
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
