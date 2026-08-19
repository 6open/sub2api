package handler

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLunaConcurrencyModelMatch(t *testing.T) {
	require.True(t, isLunaConcurrencyLimitedModel("gpt-5.6-luna"))
	require.True(t, isLunaConcurrencyLimitedModel(" GPT-5.6-LUNA "))
	require.False(t, isLunaConcurrencyLimitedModel("gpt-5.6-sol"))
}

func TestLunaConcurrencySlotNamespace(t *testing.T) {
	require.Equal(t, int64(math.MinInt64+1), lunaConcurrencySlotID(1))
	require.Equal(t, int64(math.MinInt64+157), lunaConcurrencySlotID(157))
	require.Equal(t, int64(math.MinInt64), lunaConcurrencySlotID(0))
	require.NotEqual(t, lunaConcurrencySlotID(1), lunaConcurrencySlotID(2))
}

func TestCombineReleaseFuncsRunsEachOncePerCombinedCall(t *testing.T) {
	first, second := 0, 0
	release := combineReleaseFuncs(func() { first++ }, func() { second++ })
	release()
	require.Equal(t, 1, first)
	require.Equal(t, 1, second)
}
