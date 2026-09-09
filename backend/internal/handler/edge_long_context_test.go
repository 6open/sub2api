package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEdgeSnapshotPreservesAccountLongContextPolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	k := &service.APIKey{ID: 108, UserID: 1, User: &service.User{ID: 1}, Group: &service.Group{ID: 2, Platform: service.PlatformOpenAI, LongContextPricingEnabled: true}}
	for _, enabled := range []bool{true, false} {
		a := &service.Account{ID: 6, Platform: service.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": enabled, "secret": "never serialize"}}
		snapshot := &edgeSnapshot{Account: edgeBillingAccount(a), Key: *k, PricingAt: time.Now()}
		raw, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "never serialize")
		var restored edgeSnapshot
		require.NoError(t, json.Unmarshal(raw, &restored))
		require.Equal(t, enabled, restored.Account.IsOpenAILongContextBillingEnabled())
		require.True(t, restored.Key.Group.LongContextPricingEnabled)
	}
}
