package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func advancedQuotaTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIAdvancedQuota.Enabled = true
	cfg.Gateway.OpenAIAdvancedQuota.WeeklyLimitUSD = 100
	cfg.Gateway.OpenAIAdvancedQuota.FallbackEffort = "medium"
	cfg.Gateway.OpenAIAdvancedQuota.ExemptModelKeywords = "terra,luna"
	return cfg
}

func TestOpenAIAdvancedQuotaEnabledFor(t *testing.T) {
	cfg := advancedQuotaTestConfig()
	user := &User{ID: 1, Role: RoleUser}
	require.True(t, OpenAIAdvancedQuotaEnabledFor(cfg, user, PlatformOpenAI, "gpt-5.6-sol"))
	require.False(t, OpenAIAdvancedQuotaEnabledFor(cfg, &User{ID: 2, Role: RoleAdmin}, PlatformOpenAI, "gpt-5.6-sol"))
	require.False(t, OpenAIAdvancedQuotaEnabledFor(cfg, user, PlatformGrok, "gpt-5.6-sol"))
	require.False(t, OpenAIAdvancedQuotaEnabledFor(cfg, user, PlatformOpenAI, "gpt-5.6-luna"))
}

func TestRewriteOpenAIReasoningEffort(t *testing.T) {
	for _, tt := range []struct{ name, body, path string }{
		{name: "nested", body: `{"reasoning":{"effort":"max","summary":"auto"}}`, path: "reasoning.effort"},
		{name: "flat", body: `{"reasoning_effort":"xhigh"}`, path: "reasoning_effort"},
		{name: "model suffix", body: `{"model":"gpt-5.6-sol-max"}`, path: "reasoning.effort"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next, changed, err := RewriteOpenAIReasoningEffort([]byte(tt.body), "medium")
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "medium", gjson.GetBytes(next, tt.path).String())
		})
	}
}

func TestOpenAIAdvancedQuotaUsageCost(t *testing.T) {
	cfg := advancedQuotaTestConfig()
	user := &User{ID: 1, Role: RoleUser}
	cost := &CostBreakdown{TotalCost: 1.25, ActualCost: 0.25}
	require.Equal(t, 0.25, OpenAIAdvancedQuotaUsageCost(cfg, user, PlatformOpenAI, "gpt-5.6-sol", "high", cost))
	require.Zero(t, OpenAIAdvancedQuotaUsageCost(cfg, user, PlatformOpenAI, "gpt-5.6-sol", "medium", cost))
	require.Zero(t, OpenAIAdvancedQuotaUsageCost(cfg, user, PlatformOpenAI, "gpt-5.6-luna", "max", cost))
	require.Zero(t, OpenAIAdvancedQuotaUsageCost(cfg, user, PlatformOpenAI, "gpt-5.6-sol", "high", nil))
}
