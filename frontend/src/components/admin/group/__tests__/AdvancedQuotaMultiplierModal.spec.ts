import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdvancedQuotaMultiplierModal from '../AdvancedQuotaMultiplierModal.vue'

const { getSettings, updateSettings, showError, showSuccess } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getSettings,
      updateSettings
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
}

describe('AdvancedQuotaMultiplierModal', () => {
  beforeEach(() => {
    getSettings.mockReset()
    updateSettings.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    getSettings.mockResolvedValue({ openai_advanced_quota_usage_multiplier: 0.2 })
    updateSettings.mockResolvedValue({ openai_advanced_quota_usage_multiplier: 0.35 })
  })

  it('loads and saves only the advanced quota multiplier', async () => {
    const wrapper = mount(AdvancedQuotaMultiplierModal, {
      props: { show: false, currentMultiplier: null },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          TotpStepUpDialog: true
        }
      }
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(getSettings).toHaveBeenCalledTimes(1)
    expect((wrapper.get('[data-testid="advanced-quota-multiplier-input"]').element as HTMLInputElement).value).toBe('0.2')

    await wrapper.get('[data-testid="advanced-quota-multiplier-input"]').setValue('0.35')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      openai_advanced_quota_usage_multiplier: 0.35
    })
    expect(wrapper.emitted('saved')).toEqual([[0.35]])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('rejects a negative multiplier without sending an update', async () => {
    const wrapper = mount(AdvancedQuotaMultiplierModal, {
      props: { show: true, currentMultiplier: 0.2 },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          TotpStepUpDialog: true
        }
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="advanced-quota-multiplier-input"]').setValue('-0.1')
    await wrapper.get('form').trigger('submit.prevent')

    expect(updateSettings).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.groups.advancedQuotaMultiplier.invalid')
  })
})
