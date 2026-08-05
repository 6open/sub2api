import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('RedeemView LDC code explanation', () => {
  it('explains general and LDC redeem code difference without LinuxDO binding copy', () => {
    const source = readFileSync(resolve(__dirname, '../RedeemView.vue'), 'utf8')
    const zh = readFileSync(resolve(__dirname, '../../../i18n/locales/zh/dashboard.ts'), 'utf8')

    expect(source).toContain('codeTypeTipTitle')
    expect(zh).toContain('普通兑换码：按兑换码面值直接增加余额、并发数或订阅权限。')
    expect(zh).toContain('LDC兑换码：按当前 lklb 账号历史 LDC 兑换额度阶梯折算')
    expect(zh).toContain('前 $10 按 10 LDC = $1')
    expect(zh).toContain('超出部分按 50 LDC = $1')
    expect(zh).not.toContain('兑换 LDC 码无需绑定 LinuxDO')
  })
})
