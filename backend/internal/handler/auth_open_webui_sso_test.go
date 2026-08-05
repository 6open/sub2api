package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/stretchr/testify/require"
)

func newOpenWebUITestHandler() *AuthHandler {
	return &AuthHandler{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret"}}}
}

func TestOpenWebUIHandoffSignVerify(t *testing.T) {
	h := newOpenWebUITestHandler()
	token, err := h.signOpenWebUIHandoff(openWebUIHandoffPayload{
		UserID:    42,
		Nonce:     "one-time-nonce",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	require.NoError(t, err)

	payload, ok := h.verifyOpenWebUIHandoff(token)
	require.True(t, ok)
	require.Equal(t, int64(42), payload.UserID)
	require.Equal(t, "one-time-nonce", payload.Nonce)
}

func TestOpenWebUIHandoffRejectsTamperedAndExpiredTokens(t *testing.T) {
	h := newOpenWebUITestHandler()
	token, err := h.signOpenWebUIHandoff(openWebUIHandoffPayload{
		UserID:    42,
		Nonce:     "one-time-nonce",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	require.NoError(t, err)

	_, ok := h.verifyOpenWebUIHandoff(token + "x")
	require.False(t, ok)

	expired, err := h.signOpenWebUIHandoff(openWebUIHandoffPayload{
		UserID:    42,
		Nonce:     "expired-nonce",
		ExpiresAt: time.Now().Add(-time.Second).Unix(),
	})
	require.NoError(t, err)
	_, ok = h.verifyOpenWebUIHandoff(expired)
	require.False(t, ok)
}

func TestConsumeOpenWebUIHandoffOnlyOnce(t *testing.T) {
	consumedKeys := make(map[string]bool)
	consume := func(_ context.Context, key string, _ time.Duration) (bool, error) {
		if consumedKeys[key] {
			return false, nil
		}
		consumedKeys[key] = true
		return true, nil
	}
	payload := &openWebUIHandoffPayload{Nonce: "single-use", ExpiresAt: time.Now().Add(time.Minute).Unix()}

	consumed, err := consumeOpenWebUIHandoff(context.Background(), consume, payload)
	require.NoError(t, err)
	require.True(t, consumed)

	consumed, err = consumeOpenWebUIHandoff(context.Background(), consume, payload)
	require.NoError(t, err)
	require.False(t, consumed)
}

func TestBuildOpenWebUIHandoffURL(t *testing.T) {
	result, err := buildOpenWebUIHandoffURL("https://chat.example.com/sso/callback?source=sub2api", "signed.token")
	require.NoError(t, err)
	require.Contains(t, result, "source=sub2api")
	require.Contains(t, result, "handoff=signed.token")

	_, err = buildOpenWebUIHandoffURL("http://chat.example.com/sso/callback", "signed.token")
	require.Error(t, err)
}

func TestValidOpenWebUIVerifySecret(t *testing.T) {
	require.True(t, validOpenWebUIVerifySecret("shared-secret", "shared-secret"))
	require.False(t, validOpenWebUIVerifySecret("wrong", "shared-secret"))
	require.False(t, validOpenWebUIVerifySecret("", "shared-secret"))
	require.False(t, validOpenWebUIVerifySecret("shared-secret", ""))
	require.False(t, validOpenWebUIVerifySecret(strings.Repeat("a", 31), strings.Repeat("a", 32)))
}
