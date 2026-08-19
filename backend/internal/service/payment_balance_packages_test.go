package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPermanentBalancePackages(t *testing.T) {
	packages := AvailableBalancePackages(1, false)
	require.Len(t, packages, 3)
	require.Equal(t, float64(90), packages[0].PayAmount)
	require.Equal(t, float64(200), packages[1].CreditAmount)
	require.Equal(t, float64(180), packages[1].PayAmount)
	require.Equal(t, float64(450), packages[2].PayAmount)
	require.InDelta(t, 0.9, packages[2].DiscountRate, 1e-9)
	require.Empty(t, packages[2].Badge)
	require.Empty(t, AvailableBalancePackages(1, true))
	require.Empty(t, AvailableBalancePackages(2, false))
}

func TestCalculateBalancePaymentBaseAppliesThresholdDiscount(t *testing.T) {
	require.Equal(t, float64(99), calculateBalancePaymentBase(99, 1, false))
	require.Equal(t, float64(90), calculateBalancePaymentBase(100, 1, false))
	require.Equal(t, float64(135), calculateBalancePaymentBase(150, 1, false))
	require.Equal(t, float64(100), calculateBalancePaymentBase(100, 1, true))
	require.Equal(t, float64(100), calculateBalancePaymentBase(100, 2, false))
}

func TestBuildBalanceDiscountPricingSnapshot(t *testing.T) {
	require.Nil(t, buildBalanceDiscountPricingSnapshot(99, 99))
	snapshot := buildBalanceDiscountPricingSnapshot(150, 135)
	require.Equal(t, "balance_threshold_discount", snapshot["pricing_type"])
	require.Equal(t, float64(150), snapshot["credited_amount"])
	require.Equal(t, float64(135), snapshot["discounted_payment_amount"])
	require.Equal(t, float64(100), snapshot["threshold"])
}

func TestResolveBalancePackageUsesServerAmounts(t *testing.T) {
	pkg, err := resolveBalancePackage("balance_500_450", 1, false)
	require.NoError(t, err)
	require.Equal(t, float64(500), pkg.CreditAmount)
	require.Equal(t, float64(450), pkg.PayAmount)
	require.Equal(t, "balance_500_450", buildBalancePackagePricingSnapshot(pkg)["package_id"])
}

func TestResolveBalancePackageMapsLegacyIDsToCurrentPrice(t *testing.T) {
	pkg, err := resolveBalancePackage("balance_200_170", 1, false)
	require.NoError(t, err)
	require.Equal(t, "balance_200_180", pkg.ID)
	require.Equal(t, float64(180), pkg.PayAmount)

	pkg, err = resolveBalancePackage("balance_500_400", 1, false)
	require.NoError(t, err)
	require.Equal(t, "balance_500_450", pkg.ID)
	require.Equal(t, float64(450), pkg.PayAmount)
}

func TestResolveBalancePackageRejectsUnavailableOrUnknown(t *testing.T) {
	_, err := resolveBalancePackage("balance_100_90", 1, true)
	require.Error(t, err)
	_, err = resolveBalancePackage("unknown", 1, false)
	require.Error(t, err)
}
