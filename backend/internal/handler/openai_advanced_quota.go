package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) applyOpenAIAdvancedQuotaPolicy(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, body []byte, model string) []byte {
	if h == nil || h.billingCacheService == nil || apiKey == nil || apiKey.User == nil ||
		!service.OpenAIAdvancedQuotaEnabledFor(h.cfg, apiKey.User, openAICompatibleRequestPlatform(c.Request.Context(), apiKey), model) {
		return body
	}
	requestedEffort := service.ExtractOpenAIReasoningEffortForQuota(body, model)
	if !service.IsOpenAIAdvancedReasoningEffort(requestedEffort) {
		return body
	}
	exhausted, err := h.billingCacheService.IsUserPlatformWeeklyQuotaExhausted(c.Request.Context(), apiKey.User.ID, service.PlatformOpenAIAdvanced)
	if err != nil {
		reqLog.Warn("openai.advanced_quota_check_failed_open", zap.String("requested_effort", requestedEffort), zap.Error(err))
		return body
	}
	if !exhausted {
		return body
	}
	fallback := strings.ToLower(strings.TrimSpace(h.cfg.Gateway.OpenAIAdvancedQuota.FallbackEffort))
	if fallback == "" {
		fallback = "medium"
	}
	next, changed, err := service.RewriteOpenAIReasoningEffort(body, fallback)
	if err != nil {
		reqLog.Warn("openai.advanced_quota_rewrite_failed_open", zap.String("requested_effort", requestedEffort), zap.Error(err))
		return body
	}
	if !changed {
		return body
	}
	c.Header("X-Sub2API-Reasoning-Effort", fallback)
	c.Header("X-Sub2API-Reasoning-Policy", "weekly_quota_exhausted")
	reqLog.Info("openai.advanced_quota_downgraded", zap.String("requested_effort", requestedEffort), zap.String("effective_effort", fallback))
	return next
}
