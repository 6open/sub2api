import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { post } }))

import { createUserApiKey } from '@/api/admin/users'

describe('admin create user API key', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: { id: 11, key: 'sk-created' } })
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111')
  })

  it('sends an idempotency key with the create request', async () => {
    const request = { name: 'bbs-production', group_id: 2, quota: 25 }
    const result = await createUserApiKey(45, request)

    expect(post).toHaveBeenCalledWith('/admin/users/45/api-keys', request, {
      headers: {
        'Idempotency-Key': 'user-api-key-create-45-11111111-1111-4111-8111-111111111111'
      }
    })
    expect(result).toEqual({ id: 11, key: 'sk-created' })
  })

  it('reuses the same operation key after an ambiguous failure', async () => {
    const request = { name: 'bbs-test', group_id: 2 }
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(createUserApiKey(46, request)).rejects.toThrow('network timeout')

    post.mockResolvedValueOnce({ data: { id: 12, key: 'sk-retried' } })
    await createUserApiKey(46, request)

    expect(post).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[1][2].headers).toEqual(post.mock.calls[0][2].headers)
  })
})
