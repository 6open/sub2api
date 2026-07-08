package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedeemCodeExpiry(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name        string
		code        RedeemCode
		wantExpired bool
		wantCanUse  bool
	}{
		{
			name:        "unused without expiry can be used",
			code:        RedeemCode{Status: StatusUnused},
			wantExpired: false,
			wantCanUse:  true,
		},
		{
			name:        "unused before expiry can be used",
			code:        RedeemCode{Status: StatusUnused, ExpiresAt: &future},
			wantExpired: false,
			wantCanUse:  true,
		},
		{
			name:        "unused after expiry cannot be used",
			code:        RedeemCode{Status: StatusUnused, ExpiresAt: &past},
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "explicit expired status is expired",
			code:        RedeemCode{Status: StatusExpired},
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "used code remains used even after expiry time",
			code:        RedeemCode{Status: StatusUsed, ExpiresAt: &past},
			wantExpired: false,
			wantCanUse:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantExpired, tt.code.IsExpiredAt(now))
			require.Equal(t, tt.wantCanUse, tt.code.CanUse())
		})
	}
}

func TestGeneratePrefixedRedeemCodeKeepsHyphenatedPrefixWithinMaxLength(t *testing.T) {
	code, err := GeneratePrefixedRedeemCode("LDC")
	require.NoError(t, err)
	require.Len(t, code, 32)
	require.Regexp(t, `^LDC-[A-F0-9]{28}$`, code)
}

func TestRedeemCodePrefixForGenerateDistinguishesKinds(t *testing.T) {
	require.Equal(t, "LDC", redeemCodePrefixForGenerate(RedeemTypeBalance, `{"code_kind":"ldc"}`))
	require.Equal(t, "GEN", redeemCodePrefixForGenerate(RedeemTypeBalance, ""))
	require.Equal(t, "CON", redeemCodePrefixForGenerate(RedeemTypeConcurrency, ""))
	require.Equal(t, "SUB", redeemCodePrefixForGenerate(RedeemTypeSubscription, ""))
	require.Equal(t, "INV", redeemCodePrefixForGenerate(RedeemTypeInvitation, ""))
}
