import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('PaymentView static purchase entries', () => {
  it('renders a Xianyu quota purchase card with the configured link', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('闲鱼购买额度')
    expect(source).toContain('https://m.tb.cn/h.8ci78UP?tk=LUDdgttOFxf')
    expect(source).not.toContain('下单后请把 lklb 账号邮箱 / 用户名发给客服处理')
  })

  it('renders a LinuxDO LDC purchase card with tiered promo copy', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('LinuxDO 积分购买')
    expect(source).toContain('handleLinuxDoShopPurchase')
    expect(source).toContain('startLinuxDoShopHandoff')
    expect(source).toContain('前 10刀额度享特惠')
    expect(source).toContain('10 LDC = 1刀')
    expect(source).toContain('超出后按 50 LDC = 1刀')
    expect(source).not.toContain('支持自定义额度')
    expect(source).not.toContain('点击购买时再认证')
    expect(source).toContain('>购买</button>')
  })
  it('uses the upstream stacked card layout with three presets', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('<!-- Recharge Account Card -->')
    expect(source).toContain('class="card p-5"')
    expect(source).toContain('class="card p-6"')
    expect(source).toContain(':amounts="promotionActive ? [10, 50, 100] : [1, 10, 100]"')
    expect(source).not.toContain('max-w-2xl')
    expect(source).not.toContain('[10, 20, 50, 100, 200, 500, 1000, 2000, 5000]')
  })

  it('renders the August Alipay half-price promotion with a per-account cap', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('支付宝限时直充 5 折')
    expect(source).toContain('8月6日－8月8日，实付 ¥100 到账 $200')
    expect(source).toContain('promotionRemaining.toFixed(2)')
    expect(source).toContain('effectiveRechargeMultiplier')
    expect(source).toContain('validAmount.value <= promotionRemaining.value')
  })

  it('groups secondary purchase routes in one compact full-width card', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('其他购买方式')
    expect(source).toContain('class="card overflow-hidden"')
    expect(source).toContain('lg:grid-cols-3')
    expect(source).toContain('lg:divide-x')
    expect(source).not.toContain('min-h-[')
  })

  it('renders the QQ group entry on the purchase page', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('Link-Label 交流群')
    expect(source).toContain('https://qm.qq.com/q/dqWgFGYbD2')
    expect(source).toContain('>加入</a>')
  })

  it('keeps the purchase page usable when built-in payment checkout is disabled', () => {
    const view = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')
    const router = readFileSync(resolve(__dirname, '../../../router/index.ts'), 'utf8')
    const sidebar = readFileSync(resolve(__dirname, '../../../components/layout/AppSidebar.vue'), 'utf8')

    expect(router).toContain("path: '/purchase'")
    expect(router).toContain('requiresPayment: false')
    expect(router).not.toContain("descriptionKey: 'purchase.description'")
    expect(sidebar).toContain("{ path: '/purchase', label: t('nav.buySubscription')")
    expect(sidebar).not.toContain("{ path: '/purchase', label: t('nav.buySubscription'), icon: RechargeSubscriptionIcon, hideInSimpleMode: true, featureFlag: flagPayment }")
    expect(view).toContain('Keep this page usable even when the built-in payment system is disabled')
  })
})
