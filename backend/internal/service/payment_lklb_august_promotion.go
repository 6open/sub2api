package service

import (
	"context"
	"fmt"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	lklbAugustPromotionID         = "lklb-2026-08-alipay-half-price"
	lklbAugustPromotionPayLimit   = 100.0
	lklbAugustPromotionMultiplier = 2.0
)

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type LKLBPaymentPromotion struct {
	Active       bool      `json:"active"`
	ID           string    `json:"id,omitempty"`
	PaymentType  string    `json:"payment_type,omitempty"`
	Multiplier   float64   `json:"multiplier,omitempty"`
	PayLimit     float64   `json:"pay_limit,omitempty"`
	PayUsed      float64   `json:"pay_used,omitempty"`
	PayRemaining float64   `json:"pay_remaining,omitempty"`
	StartsAt     time.Time `json:"starts_at,omitempty"`
	EndsAt       time.Time `json:"ends_at,omitempty"`
}

func lklbAugustPromotionWindow() (time.Time, time.Time) {
	return time.Date(2026, time.August, 5, 0, 0, 0, 0, shanghaiLocation),
		time.Date(2026, time.August, 10, 0, 0, 0, 0, shanghaiLocation)
}

func isLKLBPromotionActive(now time.Time) bool {
	start, end := lklbAugustPromotionWindow()
	return !now.Before(start) && now.Before(end)
}

func isLKLBAlipayPromotionRequest(req CreateOrderRequest, now time.Time) bool {
	return req.OrderType == payment.OrderTypeBalance &&
		payment.GetBasePaymentType(req.PaymentType) == payment.TypeAlipay &&
		isLKLBPromotionActive(now)
}

func lklbPromotionSnapshot(snapshot map[string]any, payAmount float64) map[string]any {
	if snapshot == nil {
		snapshot = make(map[string]any)
	}
	start, end := lklbAugustPromotionWindow()
	snapshot["promotion_id"] = lklbAugustPromotionID
	snapshot["promotion_multiplier"] = lklbAugustPromotionMultiplier
	snapshot["promotion_pay_amount"] = payAmount
	snapshot["promotion_starts_at"] = start.Format(time.RFC3339)
	snapshot["promotion_ends_at"] = end.Format(time.RFC3339)
	return snapshot
}

func isLKLBPromotionOrder(order *dbent.PaymentOrder) bool {
	return order != nil && psSnapshotStringValue(order.ProviderSnapshot["promotion_id"]) == lklbAugustPromotionID
}

func lklbPromotionUsedAmount(orders []*dbent.PaymentOrder, now time.Time) float64 {
	total := decimal.Zero
	for _, order := range orders {
		if !isLKLBPromotionOrder(order) {
			continue
		}
		if order.Status == OrderStatusPending && !order.ExpiresAt.After(now) {
			continue
		}
		total = total.Add(decimal.NewFromFloat(order.PayAmount))
	}
	return total.Round(2).InexactFloat64()
}

func (s *PaymentService) applyLKLBPromotionInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, now time.Time, requestedPay float64, orderAmount *float64, expiresAt *time.Time, snapshot *map[string]any) error {
	if !isLKLBAlipayPromotionRequest(req, now) {
		return nil
	}
	if math.IsNaN(requestedPay) || math.IsInf(requestedPay, 0) || requestedPay <= 0 {
		return infraerrors.BadRequest("INVALID_AMOUNT", "promotion payment amount must be positive")
	}
	if _, err := tx.User.Query().Where(user.IDEQ(req.UserID)).ForUpdate().Only(ctx); err != nil {
		return fmt.Errorf("lock promotion user: %w", err)
	}
	orders, err := tx.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(req.UserID),
		paymentorder.CreatedAtGTE(lklbPromotionStart()),
		paymentorder.StatusIn(OrderStatusPending, OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("query promotion orders: %w", err)
	}
	used := lklbPromotionUsedAmount(orders, now)
	remaining := decimal.NewFromFloat(lklbAugustPromotionPayLimit).Sub(decimal.NewFromFloat(used)).Round(2).InexactFloat64()
	if requestedPay-remaining > amountToleranceCNY {
		return infraerrors.BadRequest("PROMOTION_LIMIT_EXCEEDED", "promotion payment limit exceeded").WithMetadata(map[string]string{
			"remaining": fmt.Sprintf("%.2f", math.Max(0, remaining)),
		})
	}
	*orderAmount = calculateCreditedBalance(requestedPay, lklbAugustPromotionMultiplier)
	*snapshot = lklbPromotionSnapshot(*snapshot, requestedPay)
	_, end := lklbAugustPromotionWindow()
	if expiresAt.After(end) {
		*expiresAt = end
	}
	return nil
}

func lklbPromotionStart() time.Time {
	start, _ := lklbAugustPromotionWindow()
	return start
}

func lklbPromotionEnd() time.Time {
	_, end := lklbAugustPromotionWindow()
	return end
}

func (s *PaymentService) GetLKLBPaymentPromotion(ctx context.Context, userID int64, now time.Time) (LKLBPaymentPromotion, error) {
	start, end := lklbAugustPromotionWindow()
	result := LKLBPaymentPromotion{
		Active: isLKLBPromotionActive(now), ID: lklbAugustPromotionID, PaymentType: payment.TypeAlipay,
		Multiplier: lklbAugustPromotionMultiplier, PayLimit: lklbAugustPromotionPayLimit, StartsAt: start, EndsAt: end,
	}
	orders, err := s.entClient.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(userID),
		paymentorder.CreatedAtGTE(start),
		paymentorder.StatusIn(OrderStatusPending, OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted),
	).All(ctx)
	if err != nil {
		return result, fmt.Errorf("query promotion usage: %w", err)
	}
	result.PayUsed = lklbPromotionUsedAmount(orders, now)
	result.PayRemaining = decimal.NewFromFloat(result.PayLimit).Sub(decimal.NewFromFloat(result.PayUsed)).Round(2).InexactFloat64()
	if result.PayRemaining < 0 {
		result.PayRemaining = 0
	}
	return result, nil
}
