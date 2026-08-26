<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <UserDashboardStats :stats="stats" :is-simple="authStore.isSimpleMode" :is-admin="authStore.isAdmin" :platform-quotas="platformQuotas" :advanced-reset-loading="advancedResetLoading" @reset-advanced-quota="showAdvancedResetDialog = true" />
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </div>
      </template>
    </div>
    <ConfirmDialog
      :show="showAdvancedResetDialog"
      :title="t('dashboard.advancedQuotaResetTitle')"
      :message="t('dashboard.advancedQuotaResetConfirm')"
      :confirm-text="t('dashboard.advancedQuotaResetButton')"
      :cancel-text="t('common.cancel')"
      @confirm="handleAdvancedQuotaReset"
      @cancel="showAdvancedResetDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'; import { useI18n } from 'vue-i18n'; import { useAuthStore } from '@/stores/auth'; import { useAppStore } from '@/stores/app'; import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'; import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'; import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'; import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import { getMyPlatformQuotas, resetMyOpenAIAdvancedQuota } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'

const authStore = useAuthStore()
const appStore = useAppStore()
const { t } = useI18n()
const stats = ref<UserStatsType | null>(null); const loading = ref(false); const loadingUsage = ref(false); const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([]); const modelStats = ref<ModelStat[]>([]); const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const showAdvancedResetDialog = ref(false)
const advancedResetLoading = ref(false)

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000))); const endDate = ref(formatDateLocalInput(new Date())); const granularity = ref('day')

const loadStats = async () => { loading.value = true; try { await authStore.refreshUser(); stats.value = await usageAPI.getDashboardStats() } catch (error) { console.error('Failed to load dashboard stats:', error) } finally { loading.value = false } }
const loadCharts = async () => { loadingCharts.value = true; try { const res = await Promise.all([usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }), usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value })]); trendData.value = res[0].trend || []; modelStats.value = res[1].models || [] } catch (error) { console.error('Failed to load charts:', error) } finally { loadingCharts.value = false } }
const loadRecent = async () => { loadingUsage.value = true; try { const res = await usageAPI.getByDateRange(startDate.value, endDate.value); recentUsage.value = res.items.slice(0, 5) } catch (error) { console.error('Failed to load recent usage:', error) } finally { loadingUsage.value = false } }
const loadPlatformQuotas = async () => { try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] } catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] } }
const refreshAll = () => { loadStats(); loadCharts(); loadRecent(); loadPlatformQuotas() }

const handleAdvancedQuotaReset = async () => {
  showAdvancedResetDialog.value = false
  if (advancedResetLoading.value) return
  advancedResetLoading.value = true
  try {
    const { platform_quota } = await resetMyOpenAIAdvancedQuota()
    const quotas = [...(platformQuotas.value ?? [])]
    const index = quotas.findIndex((item) => item.platform === platform_quota.platform)
    if (index >= 0) quotas[index] = platform_quota
    else quotas.push(platform_quota)
    platformQuotas.value = quotas
    window.dispatchEvent(new CustomEvent('advanced-quota-updated'))
    appStore.showSuccess(t('dashboard.advancedQuotaResetSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('dashboard.advancedQuotaResetFailed')))
  } finally {
    advancedResetLoading.value = false
  }
}

onMounted(() => { refreshAll() })
</script>
