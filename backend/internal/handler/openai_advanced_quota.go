package handler

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// Used before HTTP forwarding and on every WebSocket turn.
func (h *OpenAIGatewayHandler) applyGPT6AdvancedQuota(c *gin.Context, apiKey *service.APIKey, body []byte, model string) ([]byte, string, error) {
	if !service.IsOpenAIAlwaysAdvancedModel(model) || apiKey == nil ||
		!service.OpenAIAdvancedQuotaEnabledFor(h.cfg, apiKey.User, openAICompatibleRequestPlatform(c.Request.Context(), apiKey), model) {
		return body, model, nil
	}
	if h.billingCacheService == nil {
		return body, model, fmt.Errorf("ADVANCED_QUOTA_UNAVAILABLE: cannot check GPT-6 advanced quota")
	}
	exhausted, err := h.billingCacheService.IsUserPlatformWeeklyQuotaExhausted(c.Request.Context(), apiKey.User.ID, service.PlatformOpenAIAdvanced)
	if err != nil {
		return body, model, fmt.Errorf("ADVANCED_QUOTA_UNAVAILABLE: cannot check GPT-6 advanced quota")
	}
	if exhausted {
		next, err := sjson.SetBytes(body, "model", "gpt-5.6-sol")
		if err != nil {
			return body, model, err
		}
		next, _, err = service.RewriteOpenAIReasoningEffort(next, "medium")
		if err != nil {
			return body, model, err
		}
		// Normalize both representations if a client supplied both.
		next, err = sjson.DeleteBytes(next, "reasoning_effort")
		if err != nil {
			return body, model, err
		}
		next, err = sjson.SetBytes(next, "reasoning.effort", "medium")
		if err != nil {
			return body, model, err
		}
		if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
			next, err = sjson.SetBytes(next, "reasoning_effort", "medium")
			if err != nil {
				return body, model, err
			}
		}
		c.Header("X-Sub2API-Model", "gpt-5.6-sol")
		c.Header("X-Sub2API-Reasoning-Effort", "medium")
		c.Header("X-Sub2API-Reasoning-Policy", "weekly_quota_exhausted")
		return next, "gpt-5.6-sol", nil
	}
	return body, model, nil
}

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
