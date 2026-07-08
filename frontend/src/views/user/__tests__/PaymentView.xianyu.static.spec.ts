import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('PaymentView static purchase entries', () => {
  it('renders a Xianyu quota purchase card with the configured link', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('闲鱼购买额度')
    expect(source).toContain('https://m.tb.cn/h.RFGn1sT?tk=UCk1gkZ6Cl5')
    expect(source).not.toContain('下单后请把 lklb 账号邮箱 / 用户名发给客服处理')
  })

  it('renders a LinuxDO LDC purchase card with tiered promo copy', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('LinuxDO 积分购买')
    expect(source).toContain('handleLinuxDoShopPurchase')
    expect(source).toContain('startLinuxDoShopHandoff')
    expect(source).toContain('前 10刀额度享特惠')
    expect(source).toContain('支持自定义额度')
    expect(source).toContain('10 LDC = 1刀')
    expect(source).toContain('超出后按 50 LDC = 1刀')
    expect(source).toContain('立即购买')
  })



  it('uses equal-size promo panels with a warm Xianyu color treatment', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('lg:grid-cols-2')
    expect(source).toContain('min-h-[210px]')
    expect(source).toContain('border-orange-200')
    expect(source).toContain('from-orange-50')
    expect(source).toContain('shadow-orange-500/20')
    expect(source).toContain('闲鱼客服购买')
  })

  it('renders the QQ group entry on the purchase page', () => {
    const source = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')

    expect(source).toContain('link-lable交流群')
    expect(source).toContain('https://qm.qq.com/q/dqWgFGYbD2')
    expect(source).toContain('点击链接加入群聊')
  })

  it('keeps the purchase page usable when built-in payment checkout is disabled', () => {
    const view = readFileSync(resolve(__dirname, '../PaymentView.vue'), 'utf8')
    const router = readFileSync(resolve(__dirname, '../../../router/index.ts'), 'utf8')
    const sidebar = readFileSync(resolve(__dirname, '../../../components/layout/AppSidebar.vue'), 'utf8')

    expect(router).toContain("path: '/purchase'")
    expect(router).toContain('requiresPayment: false')
    expect(sidebar).toContain("{ path: '/purchase', label: t('nav.buySubscription')")
    expect(sidebar).not.toContain("{ path: '/purchase', label: t('nav.buySubscription'), icon: RechargeSubscriptionIcon, hideInSimpleMode: true, featureFlag: flagPayment }")
    expect(view).toContain('Keep this page usable even when the built-in payment system is disabled')
  })
})
