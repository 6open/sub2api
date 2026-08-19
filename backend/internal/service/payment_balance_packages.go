package service

import (
	"math"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const balancePackagePricingVersion = 1

// BalancePackage is a server-owned fixed-price balance offer. Client-supplied
// amounts are never used when a package ID is selected.
type BalancePackage struct {
	ID           string  `json:"id"`
	CreditAmount float64 `json:"credit_amount"`
	PayAmount    float64 `json:"pay_amount"`
	DiscountRate float64 `json:"discount_rate"`
	Badge        string  `json:"badge,omitempty"`
}

type BalancePricingSnapshot struct {
	PricingType             string  `json:"pricing_type"`
	PackageID               string  `json:"package_id"`
	CreditedAmount          float64 `json:"credited_amount"`
	BasePaymentAmount       float64 `json:"base_payment_amount"`
	DiscountRate            float64 `json:"discount_rate"`
	DiscountedPaymentAmount float64 `json:"discounted_payment_amount"`
	CampaignID              string  `json:"campaign_id"`
	Stackable               bool    `json:"stackable"`
	Version                 int     `json:"version"`
}

var permanentBalancePackages = []BalancePackage{
	{ID: "balance_100_90", CreditAmount: 100, PayAmount: 90, DiscountRate: 0.90},
	{ID: "balance_200_170", CreditAmount: 200, PayAmount: 170, DiscountRate: 0.85, Badge: "recommended"},
	{ID: "balance_500_400", CreditAmount: 500, PayAmount: 400, DiscountRate: 0.80, Badge: "best_value"},
}

func AvailableBalancePackages(balanceRechargeMultiplier float64) []BalancePackage {
	if !permanentBalancePackagesEnabled(balanceRechargeMultiplier) {
		return []BalancePackage{}
	}
	result := make([]BalancePackage, len(permanentBalancePackages))
	copy(result, permanentBalancePackages)
	return result
}

func permanentBalancePackagesEnabled(balanceRechargeMultiplier float64) bool {
	return math.Abs(normalizeBalanceRechargeMultiplier(balanceRechargeMultiplier)-1) < 1e-9
}

func resolveBalancePackage(packageID string, balanceRechargeMultiplier float64) (*BalancePackage, error) {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" {
		return nil, nil
	}
	if !permanentBalancePackagesEnabled(balanceRechargeMultiplier) {
		return nil, infraerrors.Conflict("BALANCE_PACKAGE_UNAVAILABLE", "fixed balance packages are unavailable while another recharge offer is active")
	}
	for _, item := range permanentBalancePackages {
		if item.ID == packageID {
			pkg := item
			return &pkg, nil
		}
	}
	return nil, infraerrors.BadRequest("INVALID_BALANCE_PACKAGE", "unknown balance package")
}

func buildBalancePackagePricingSnapshot(pkg *BalancePackage) map[string]any {
	if pkg == nil {
		return nil
	}
	snapshot := BalancePricingSnapshot{
		PricingType:             "permanent_package",
		PackageID:               pkg.ID,
		CreditedAmount:          pkg.CreditAmount,
		BasePaymentAmount:       pkg.CreditAmount,
		DiscountRate:            pkg.DiscountRate,
		DiscountedPaymentAmount: pkg.PayAmount,
		CampaignID:              "",
		Stackable:               false,
		Version:                 balancePackagePricingVersion,
	}
	return map[string]any{
		"pricing_type":              snapshot.PricingType,
		"package_id":                snapshot.PackageID,
		"credited_amount":           snapshot.CreditedAmount,
		"base_payment_amount":       snapshot.BasePaymentAmount,
		"discount_rate":             snapshot.DiscountRate,
		"discounted_payment_amount": snapshot.DiscountedPaymentAmount,
		"campaign_id":               snapshot.CampaignID,
		"stackable":                 snapshot.Stackable,
		"version":                   snapshot.Version,
	}
}
