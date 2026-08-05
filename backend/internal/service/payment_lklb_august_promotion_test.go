//go:build unit

package service

import (
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestLKLBAugustPromotionWindowAndEligibility(t *testing.T) {
	start, end := lklbAugustPromotionWindow()
	base := CreateOrderRequest{OrderType: payment.OrderTypeBalance, PaymentType: payment.TypeAlipay}

	require.False(t, isLKLBAlipayPromotionRequest(base, start.Add(-time.Nanosecond)))
	require.True(t, isLKLBAlipayPromotionRequest(base, start))
	require.True(t, isLKLBAlipayPromotionRequest(base, end.Add(-time.Nanosecond)))
	require.False(t, isLKLBAlipayPromotionRequest(base, end))
	require.False(t, isLKLBAlipayPromotionRequest(CreateOrderRequest{OrderType: payment.OrderTypeSubscription, PaymentType: payment.TypeAlipay}, start))
	require.False(t, isLKLBAlipayPromotionRequest(CreateOrderRequest{OrderType: payment.OrderTypeBalance, PaymentType: payment.TypeWxpay}, start))
	require.Equal(t, "2026-08-06T00:00:00+08:00", start.Format(time.RFC3339))
	require.Equal(t, "2026-08-09T00:00:00+08:00", end.Format(time.RFC3339))
}

func TestLKLBPromotionUsedAmountCountsLiveReservationsAndCompletedOrders(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, shanghaiLocation)
	orders := []*dbent.PaymentOrder{
		promotionTestOrder(OrderStatusPending, 10, now.Add(time.Minute)),
		promotionTestOrder(OrderStatusPending, 20, now.Add(-time.Second)),
		promotionTestOrder(OrderStatusCompleted, 30, now.Add(-time.Hour)),
		{Status: OrderStatusCompleted, PayAmount: 40, ProviderSnapshot: map[string]any{}},
	}

	require.Equal(t, 40.0, lklbPromotionUsedAmount(orders, now))
}

func TestLKLBPromotionSnapshotAndAffiliateBase(t *testing.T) {
	snapshot := lklbPromotionSnapshot(map[string]any{"provider_key": payment.TypeAlipay}, 100)
	order := &dbent.PaymentOrder{
		OrderType:        payment.OrderTypeBalance,
		Amount:           200,
		PayAmount:        100,
		ProviderSnapshot: snapshot,
	}

	require.Equal(t, lklbAugustPromotionID, snapshot["promotion_id"])
	require.Equal(t, 2.0, snapshot["promotion_multiplier"])
	require.Equal(t, 100.0, snapshot["promotion_pay_amount"])
	require.True(t, isLKLBPromotionOrder(order))
	require.Equal(t, 100.0, affiliateRebateBaseAmount(order))
}

func promotionTestOrder(status string, payAmount float64, expiresAt time.Time) *dbent.PaymentOrder {
	return &dbent.PaymentOrder{
		Status: status, PayAmount: payAmount, ExpiresAt: expiresAt,
		ProviderSnapshot: map[string]any{"promotion_id": lklbAugustPromotionID},
	}
}
