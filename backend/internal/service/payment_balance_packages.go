package service

import (
	"math"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	balancePackagePricingVersion = 3
	BalanceDiscountThreshold     = 100.0
	BalanceDiscountRate          = 0.90
)

type BalancePackage struct {
	ID           string  `json:"id"`
	CreditAmount float64 `json:"credit_amount"`
	PayAmount    float64 `json:"pay_amount"`
	DiscountRate float64 `json:"discount_rate"`
	Badge        string  `json:"badge,omitempty"`
}

var permanentBalancePackages = []BalancePackage{
	{ID: "balance_100_90", CreditAmount: 100, PayAmount: 90, DiscountRate: 0.90},
	{ID: "balance_200_180", CreditAmount: 200, PayAmount: 180, DiscountRate: 0.90, Badge: "recommended"},
	{ID: "balance_500_450", CreditAmount: 500, PayAmount: 450, DiscountRate: 0.90},
}

func AvailableBalancePackages(balanceRechargeMultiplier float64, promotionActive bool) []BalancePackage {
	if !BalanceDiscountAvailable(balanceRechargeMultiplier, promotionActive) {
		return []BalancePackage{}
	}
	result := make([]BalancePackage, len(permanentBalancePackages))
	copy(result, permanentBalancePackages)
	return result
}

func BalanceDiscountAvailable(balanceRechargeMultiplier float64, promotionActive bool) bool {
	return !promotionActive && math.Abs(normalizeBalanceRechargeMultiplier(balanceRechargeMultiplier)-1) < 1e-9
}

func calculateBalancePaymentBase(creditedAmount, balanceRechargeMultiplier float64, promotionActive bool) float64 {
	if creditedAmount >= BalanceDiscountThreshold && BalanceDiscountAvailable(balanceRechargeMultiplier, promotionActive) {
		return math.Round(creditedAmount*BalanceDiscountRate*100) / 100
	}
	return creditedAmount
}

func resolveBalancePackage(packageID string, balanceRechargeMultiplier float64, promotionActive bool) (*BalancePackage, error) {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" {
		return nil, nil
	}
	if !BalanceDiscountAvailable(balanceRechargeMultiplier, promotionActive) {
		return nil, infraerrors.Conflict("BALANCE_PACKAGE_UNAVAILABLE", "fixed balance packages are unavailable while another recharge offer is active")
	}
	// Accept IDs emitted by the initial tiered-price UI, but always resolve them
	// to the current 10%-off server price for already-open browser sessions.
	switch packageID {
	case "balance_200_170":
		packageID = "balance_200_180"
	case "balance_500_400":
		packageID = "balance_500_450"
	}
	for _, item := range permanentBalancePackages {
		if item.ID == packageID {
			pkg := item
			return &pkg, nil
		}
	}
	return nil, infraerrors.BadRequest("INVALID_BALANCE_PACKAGE", "unknown balance package")
}

func buildBalanceDiscountPricingSnapshot(creditedAmount, paymentBase float64) map[string]any {
	if creditedAmount < BalanceDiscountThreshold || paymentBase >= creditedAmount {
		return nil
	}
	return map[string]any{
		"pricing_type": "balance_threshold_discount", "threshold": BalanceDiscountThreshold,
		"credited_amount": creditedAmount, "base_payment_amount": creditedAmount,
		"discount_rate": BalanceDiscountRate, "discounted_payment_amount": paymentBase,
		"campaign_id": "", "stackable": false, "version": balancePackagePricingVersion,
	}
}

func mergePricingSnapshot(snapshot, pricing map[string]any) map[string]any {
	if len(pricing) == 0 {
		return snapshot
	}
	if snapshot == nil {
		snapshot = make(map[string]any, len(pricing))
	}
	for key, value := range pricing {
		snapshot[key] = value
	}
	return snapshot
}

func buildBalancePackagePricingSnapshot(pkg *BalancePackage) map[string]any {
	if pkg == nil {
		return nil
	}
	return map[string]any{
		"pricing_type": "permanent_package", "package_id": pkg.ID,
		"credited_amount": pkg.CreditAmount, "base_payment_amount": pkg.CreditAmount,
		"discount_rate": pkg.DiscountRate, "discounted_payment_amount": pkg.PayAmount,
		"campaign_id": "", "stackable": false, "version": balancePackagePricingVersion,
	}
}
