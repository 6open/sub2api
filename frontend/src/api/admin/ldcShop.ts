/**
 * Admin LDC shop (LinuxDO credit code shop) order APIs
 */

import { apiClient } from '../client'

export interface LDCShopOrder {
  id: number
  out_trade_no: string
  plan: string
  ldc_amount: string
  usd_value: number
  user_sub: string
  username: string
  status: string
  credit_trade_no: string
  code: string
  sub2api_user_id: number | null
  sub2api_user_email: string
  delivery_message: string
  created_at: string
  updated_at: string
}

export interface LDCShopOrdersResponse {
  items: LDCShopOrder[]
  total: number
  page: number
  page_size: number
  pages: number
  status_counts: Record<string, number>
}

export async function listOrders(params?: {
  page?: number
  page_size?: number
  status?: string
  search?: string
}): Promise<LDCShopOrdersResponse> {
  const { data } = await apiClient.get<LDCShopOrdersResponse>('/admin/ldc-shop/orders', { params })
  return {
    items: data?.items || [],
    total: data?.total || 0,
    page: data?.page || 1,
    page_size: data?.page_size || 20,
    pages: data?.pages || 1,
    status_counts: data?.status_counts || {}
  }
}

export const ldcShopAPI = {
  listOrders
}

export default ldcShopAPI
