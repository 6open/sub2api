import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  getUserApiKeys: vi.fn(),
  createUserApiKey: vi.fn(),
  getAllGroups: vi.fn(),
  updateApiKeyGroup: vi.fn(),
  copyToClipboard: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      getUserApiKeys: mocks.getUserApiKeys,
      createUserApiKey: mocks.createUserApiKey,
    },
    groups: { getAll: mocks.getAllGroups },
    apiKeys: { updateApiKeyGroup: mocks.updateApiKeyGroup },
  },
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copyToClipboard }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import UserApiKeysModal from '../UserApiKeysModal.vue'

const existingKey = {
  id: 10,
  user_id: 45,
  key: 'sk-existing-public-service-key-12345678',
  name: 'existing-service',
  group_id: 2,
  status: 'active',
  created_at: '2026-08-27T00:00:00Z',
  updated_at: '2026-08-27T00:00:00Z',
}

const createdKey = {
  ...existingKey,
  id: 11,
  key: 'sk-new-bbs-production-key-87654321',
  name: 'bbs-production',
}

const user = {
  id: 45,
  email: 'public-service@migomigorobot.com',
  username: '公共服务',
  allowed_groups: [],
}

async function mountAndOpen() {
  const wrapper = mount(UserApiKeysModal, {
    props: { show: false, user: user as any },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          template: '<div v-if="show"><slot /></div>',
        },
        Icon: { props: ['name'], template: '<span :data-icon="name" />' },
        GroupBadge: true,
        GroupOptionItem: true,
        Teleport: true,
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getUserApiKeys.mockResolvedValue({ items: [existingKey], total: 1 })
  mocks.getAllGroups.mockResolvedValue([{
    id: 2,
    name: 'gpt20x',
    platform: 'openai',
    status: 'active',
    subscription_type: 'standard',
    is_exclusive: false,
    rate_multiplier: 0.2,
  }])
  mocks.createUserApiKey.mockResolvedValue(createdKey)
  mocks.copyToClipboard.mockResolvedValue(true)
})

describe('UserApiKeysModal', () => {
  it('masks keys by default and lets an admin reveal and copy the full value', async () => {
    const wrapper = await mountAndOpen()

    expect(wrapper.text()).not.toContain(existingKey.key)
    expect(wrapper.text()).toContain('sk-existing-...12345678')

    await wrapper.get('[data-test="toggle-key-10"]').trigger('click')
    expect(wrapper.text()).toContain(existingKey.key)

    await wrapper.get('[data-test="copy-key-10"]').trigger('click')
    await flushPromises()
    expect(mocks.copyToClipboard).toHaveBeenCalledWith(existingKey.key, 'admin.users.apiKeyCopied')
  })

  it('creates a key for the selected user and reveals it immediately', async () => {
    const wrapper = await mountAndOpen()
    await wrapper.get('[data-test="toggle-create-key"]').trigger('click')
    await wrapper.get('[data-test="create-key-name"]').setValue('bbs-production')
    await wrapper.get('[data-test="create-key-group"]').setValue('2')
    await wrapper.get('[data-test="create-key-quota"]').setValue('25')
    await wrapper.get('[data-test="create-key-expiry"]').setValue('90')
    await wrapper.get('[data-test="create-key-ip-whitelist"]').setValue('203.0.113.10, 2001:db8::/64')
    await wrapper.get('[data-test="create-key-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.createUserApiKey).toHaveBeenCalledWith(45, {
      name: 'bbs-production',
      group_id: 2,
      quota: 25,
      expires_in_days: 90,
      ip_whitelist: ['203.0.113.10', '2001:db8::/64'],
    })
    expect(wrapper.text()).toContain(createdKey.key)
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.users.apiKeyCreated')
  })
})
