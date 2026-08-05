import { apiClient } from './client'

export interface OpenWebUIHandoffResponse {
  url: string
}

export async function startOpenWebUIHandoff(): Promise<OpenWebUIHandoffResponse> {
  const response = await apiClient.post<OpenWebUIHandoffResponse>('/auth/open-webui/handoff/start')
  return response.data
}

export const openWebUIAPI = {
  startHandoff: startOpenWebUIHandoff
}
