package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http/httptest"
	"testing"
	"time"
)

type gpt6QuotaRepo struct {
	service.UserPlatformQuotaRepository
	used float64
}

func (r *gpt6QuotaRepo) GetByUserPlatform(context.Context, int64, string) (*service.UserPlatformQuotaRecord, error) {
	limit := 100.0
	now := time.Now()
	return &service.UserPlatformQuotaRecord{WeeklyLimitUSD: &limit, WeeklyUsageUSD: r.used, WeeklyWindowStart: &now}, nil
}

func TestGPT6AdvancedQuotaGate(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIAdvancedQuota.Enabled = true
	cfg.Gateway.OpenAIAdvancedQuota.WeeklyLimitUSD = 100
	for _, used := range []float64{99, 100, 101} {
		cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, &gpt6QuotaRepo{used: used})
		h := &OpenAIGatewayHandler{cfg: cfg, billingCacheService: cache}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		key := &service.APIKey{User: &service.User{ID: 1, Role: service.RoleUser}, Group: &service.Group{Platform: service.PlatformOpenAI}}
		body := []byte(`{"model":"gpt-6-astra","reasoning_effort":"high","input":"hello"}`)
		next, model, err := h.applyGPT6AdvancedQuota(c, key, body, "gpt-6-astra")
		require.NoError(t, err)
		if used >= 100 {
			require.Equal(t, "gpt-5.6-sol", model)
			require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(next, "model").String())
			require.Equal(t, "medium", gjson.GetBytes(next, "reasoning.effort").String())
			require.False(t, gjson.GetBytes(next, "reasoning_effort").Exists())
			require.Zero(t, service.OpenAIAdvancedQuotaUsageCost(cfg, key.User, service.PlatformOpenAI, model, "medium", &service.CostBreakdown{TotalCost: 10}, 0.2))
		} else {
			require.Equal(t, body, next)
			require.Equal(t, "gpt-6-astra", model)
		}
		for _, effort := range []string{"", "low", "medium", "high", "max"} {
			for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
				c.Request = httptest.NewRequest("POST", path, nil)
				raw := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"` + effort + `"},"input":"hello"}`)
				out, effective, e := h.applyGPT6AdvancedQuota(c, key, raw, "gpt-6-astra")
				require.NoError(t, e)
				if used >= 100 {
					require.Equal(t, "gpt-5.6-sol", effective)
					require.Equal(t, "medium", gjson.GetBytes(out, "reasoning.effort").String())
					if path == "/v1/chat/completions" {
						require.Equal(t, "medium", gjson.GetBytes(out, "reasoning_effort").String())
					}
				} else {
					require.Equal(t, raw, out)
				}
			}
		}
		_, model, err = h.applyGPT6AdvancedQuota(c, key, body, "gpt-5.6-sol")
		require.NoError(t, err)
		require.Equal(t, "gpt-5.6-sol", model)
		key.User.Role = service.RoleAdmin
		next, model, err = h.applyGPT6AdvancedQuota(c, key, body, "gpt-6-astra")
		require.NoError(t, err)
		require.Equal(t, body, next)
		require.Equal(t, "gpt-6-astra", model)
		cache.Stop()
	}
}
