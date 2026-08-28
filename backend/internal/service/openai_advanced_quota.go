package service

import (
	"math"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func IsOpenAIAdvancedReasoningEffort(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high", "xhigh", "x-high", "x_high", "max":
		return true
	default:
		return false
	}
}

func ExtractOpenAIReasoningEffortForQuota(body []byte, modelCandidates ...string) string {
	effort := extractOpenAIReasoningEffortFromBody(body, modelCandidates...)
	if effort == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*effort))
}

func OpenAIAdvancedQuotaModelExempt(cfg *config.Config, models ...string) bool {
	if cfg == nil {
		return false
	}
	for _, rawKeyword := range strings.Split(cfg.Gateway.OpenAIAdvancedQuota.ExemptModelKeywords, ",") {
		keyword := strings.ToLower(strings.TrimSpace(rawKeyword))
		if keyword == "" {
			continue
		}
		for _, model := range models {
			if strings.Contains(strings.ToLower(strings.TrimSpace(model)), keyword) {
				return true
			}
		}
	}
	return false
}

func OpenAIAdvancedQuotaEnabledFor(cfg *config.Config, user *User, platform string, models ...string) bool {
	return cfg != nil && cfg.Gateway.OpenAIAdvancedQuota.Enabled &&
		cfg.Gateway.OpenAIAdvancedQuota.WeeklyLimitUSD > 0 && user != nil &&
		!user.IsAdmin() && platform == PlatformOpenAI &&
		!OpenAIAdvancedQuotaModelExempt(cfg, models...)
}

func RewriteOpenAIReasoningEffort(body []byte, effort string) ([]byte, bool, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		effort = "medium"
	}
	if current := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String()); current != "" {
		if strings.EqualFold(current, effort) {
			return body, false, nil
		}
		next, err := sjson.SetBytes(body, "reasoning.effort", effort)
		return next, err == nil, err
	}
	if current := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String()); current != "" {
		if strings.EqualFold(current, effort) {
			return body, false, nil
		}
		next, err := sjson.SetBytes(body, "reasoning_effort", effort)
		return next, err == nil, err
	}
	next, err := sjson.SetBytes(body, "reasoning.effort", effort)
	return next, err == nil, err
}

func OpenAIAdvancedQuotaUsageCost(cfg *config.Config, user *User, platform, model, effort string, cost *CostBreakdown, multiplier float64) float64 {
	if cost == nil || cost.TotalCost <= 0 || !IsOpenAIAdvancedReasoningEffort(effort) ||
		!OpenAIAdvancedQuotaEnabledFor(cfg, user, platform, model) {
		return 0
	}
	if multiplier < 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		multiplier = DefaultOpenAIAdvancedQuotaUsageMultiplier
	}
	return cost.TotalCost * multiplier
}
