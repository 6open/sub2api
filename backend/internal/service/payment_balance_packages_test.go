package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAvailableBalancePackages(t *testing.T) {
	packages := AvailableBalancePackages(1)
	require.Equal(t, []BalancePackage{
		{ID: "balance_100_90", CreditAmount: 100, PayAmount: 90, DiscountRate: 0.90},
		{ID: "balance_200_170", CreditAmount: 200, PayAmount: 170, DiscountRate: 0.85, Badge: "recommended"},
		{ID: "balance_500_400", CreditAmount: 500, PayAmount: 400, DiscountRate: 0.80, Badge: "best_value"},
	}, packages)
}

func TestAvailableBalancePackagesDisabledDuringMultiplierOffer(t *testing.T) {
	require.Empty(t, AvailableBalancePackages(2))
	_, err := resolveBalancePackage("balance_100_90", 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "another recharge offer is active")
}

func TestResolveBalancePackageUsesServerOwnedAmounts(t *testing.T) {
	pkg, err := resolveBalancePackage("balance_500_400", 1)
	require.NoError(t, err)
	require.Equal(t, float64(500), pkg.CreditAmount)
	require.Equal(t, float64(400), pkg.PayAmount)

	snapshot := buildBalancePackagePricingSnapshot(pkg)
	require.Equal(t, "permanent_package", snapshot["pricing_type"])
	require.Equal(t, "balance_500_400", snapshot["package_id"])
	require.Equal(t, float64(500), snapshot["credited_amount"])
	require.Equal(t, float64(400), snapshot["discounted_payment_amount"])
	require.Equal(t, false, snapshot["stackable"])
	require.Equal(t, balancePackagePricingVersion, snapshot["version"])
}

func TestResolveBalancePackageRejectsUnknownID(t *testing.T) {
	_, err := resolveBalancePackage("balance_500_1", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown balance package")
}
