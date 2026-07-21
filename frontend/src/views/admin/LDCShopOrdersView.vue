<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="card p-4">
          <div class="flex flex-wrap items-center gap-3">
            <div class="flex-1 sm:max-w-72">
              <input
                v-model="search"
                type="text"
                class="input"
                :placeholder="t('admin.ldcShop.searchPlaceholder')"
                @input="debounceLoad"
              />
            </div>
            <Select
              v-model="status"
              :options="statusOptions"
              class="w-40"
              @change="reloadFirstPage"
            />
            <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
              <button
                class="btn btn-secondary"
                :disabled="loading"
                :title="t('common.refresh')"
                @click="loadOrders"
              >
                <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              </button>
            </div>
          </div>
          <div v-if="statusSummary" class="mt-3 flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span
              v-for="(count, key) in statusCounts"
              :key="key"
              class="rounded-full bg-gray-100 px-2 py-1 dark:bg-dark-700"
            >
              {{ statusLabel(key) }}: {{ count }}
            </span>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="orders" :loading="loading">
          <template #cell-out_trade_no="{ value }">
            <span class="font-mono text-xs text-gray-900 dark:text-white">{{ value }}</span>
          </template>
          <template #cell-username="{ row }">
            <div class="min-w-0">
              <div class="text-sm font-medium text-gray-900 dark:text-white">{{ row.username || '-' }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">{{ row.user_sub || '-' }}</div>
            </div>
          </template>
          <template #cell-ldc_amount="{ value, row }">
            <div class="text-sm text-gray-900 dark:text-white">
              {{ value }} LDC
              <span class="text-xs text-gray-500 dark:text-gray-400">/ ${{ Number(row.usd_value || 0).toFixed(2) }}</span>
            </div>
          </template>
          <template #cell-status="{ value }">
            <span :class="['badge', statusBadgeClass(value)]">{{ statusLabel(value) }}</span>
          </template>
          <template #cell-sub2api_user="{ row }">
            <div class="text-sm text-gray-700 dark:text-gray-300">
              <template v-if="row.sub2api_user_id">#{{ row.sub2api_user_id }}</template>
              <template v-else>-</template>
              <div v-if="row.sub2api_user_email" class="max-w-48 truncate text-xs text-gray-500 dark:text-gray-400">
                {{ row.sub2api_user_email }}
              </div>
            </div>
          </template>
          <template #cell-code="{ value }">
            <span class="font-mono text-xs text-gray-600 dark:text-gray-300">{{ value || '-' }}</span>
          </template>
          <template #cell-delivery_message="{ value }">
            <span class="block max-w-56 truncate text-xs text-gray-500 dark:text-gray-400" :title="value || ''">
              {{ value || '-' }}
            </span>
          </template>
          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(value) }}</span>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { ldcShopAPI, type LDCShopOrder } from '@/api/admin/ldcShop'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const orders = ref<LDCShopOrder[]>([])
const search = ref('')
const status = ref('')
const statusCounts = ref<Record<string, number>>({})
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const columns = computed(() => [
  { key: 'out_trade_no', label: t('admin.ldcShop.columns.orderNo') },
  { key: 'username', label: t('admin.ldcShop.columns.linuxdoUser') },
  { key: 'ldc_amount', label: t('admin.ldcShop.columns.amount') },
  { key: 'status', label: t('admin.ldcShop.columns.status') },
  { key: 'sub2api_user', label: t('admin.ldcShop.columns.sub2apiUser') },
  { key: 'code', label: t('admin.ldcShop.columns.code') },
  { key: 'delivery_message', label: t('admin.ldcShop.columns.delivery') },
  { key: 'created_at', label: t('admin.ldcShop.columns.createdAt') }
])

const statusOptions = computed(() => [
  { value: '', label: t('admin.ldcShop.allStatus') },
  { value: 'credited', label: t('admin.ldcShop.status.credited') },
  { value: 'issued', label: t('admin.ldcShop.status.issued') },
  { value: 'expired', label: t('admin.ldcShop.status.expired') },
  { value: 'created', label: t('admin.ldcShop.status.created') },
  { value: 'create_failed', label: t('admin.ldcShop.status.create_failed') },
  { value: 'pending_no_stock', label: t('admin.ldcShop.status.pending_no_stock') }
])

const statusSummary = computed(() => Object.keys(statusCounts.value || {}).length > 0)

function statusLabel(value: string): string {
  const key = `admin.ldcShop.status.${value}`
  const translated = t(key)
  return translated === key ? value || '-' : translated
}

function statusBadgeClass(value: string): string {
  switch (value) {
    case 'credited':
      return 'badge-success'
    case 'issued':
      return 'badge-warning'
    case 'expired':
    case 'create_failed':
    case 'pending_no_stock':
      return 'badge-danger'
    default:
      return 'badge-gray'
  }
}

function formatDateTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoad() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    pagination.page = 1
    loadOrders()
  }, 300)
}

function reloadFirstPage() {
  pagination.page = 1
  loadOrders()
}

async function loadOrders() {
  loading.value = true
  try {
    const res = await ldcShopAPI.listOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      status: status.value || undefined,
      search: search.value || undefined
    })
    orders.value = res.items
    pagination.total = res.total
    statusCounts.value = res.status_counts || {}
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'admin.ldcShop', t('admin.ldcShop.failedToLoad')))
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  loadOrders()
}

function handlePageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  loadOrders()
}

onMounted(() => {
  loadOrders()
})
</script>
