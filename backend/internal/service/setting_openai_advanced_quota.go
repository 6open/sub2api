package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOpenAIAdvancedQuotaUsageMultiplier = 0.2
	openAIAdvancedQuotaMultiplierCacheTTL     = 60 * time.Second
	openAIAdvancedQuotaMultiplierErrorTTL     = 5 * time.Second
	openAIAdvancedQuotaMultiplierDBTimeout    = 5 * time.Second
)

type cachedOpenAIAdvancedQuotaMultiplier struct {
	value     float64
	expiresAt int64
}

func parseOpenAIAdvancedQuotaUsageMultiplier(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return DefaultOpenAIAdvancedQuotaUsageMultiplier
	}
	return value
}

// GetOpenAIAdvancedQuotaUsageMultiplier returns the independently configured
// multiplier for advanced-quota accounting. It is cached because this method
// runs once for every completed high-reasoning request.
func (s *SettingService) GetOpenAIAdvancedQuotaUsageMultiplier(ctx context.Context) float64 {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAIAdvancedQuotaUsageMultiplier
	}
	if cached, ok := s.openAIAdvancedQuotaMultiplierCache.Load().(*cachedOpenAIAdvancedQuotaMultiplier); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}

	result, _, _ := s.openAIAdvancedQuotaMultiplierSF.Do(SettingKeyOpenAIAdvancedQuotaUsageMultiplier, func() (any, error) {
		if cached, ok := s.openAIAdvancedQuotaMultiplierCache.Load().(*cachedOpenAIAdvancedQuotaMultiplier); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAdvancedQuotaMultiplierDBTimeout)
		defer cancel()

		raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIAdvancedQuotaUsageMultiplier)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("failed to get OpenAI advanced quota usage multiplier", "error", err)
			fallback := DefaultOpenAIAdvancedQuotaUsageMultiplier
			if prior, ok := s.openAIAdvancedQuotaMultiplierCache.Load().(*cachedOpenAIAdvancedQuotaMultiplier); ok && prior != nil {
				fallback = prior.value
			}
			s.storeOpenAIAdvancedQuotaMultiplierCache(fallback, openAIAdvancedQuotaMultiplierErrorTTL)
			return fallback, nil
		}

		value := parseOpenAIAdvancedQuotaUsageMultiplier(raw)
		s.storeOpenAIAdvancedQuotaMultiplierCache(value, openAIAdvancedQuotaMultiplierCacheTTL)
		return value, nil
	})
	if value, ok := result.(float64); ok {
		return value
	}
	return DefaultOpenAIAdvancedQuotaUsageMultiplier
}

func (s *SettingService) storeOpenAIAdvancedQuotaMultiplierCache(value float64, ttl time.Duration) {
	value = parseOpenAIAdvancedQuotaUsageMultiplier(strconv.FormatFloat(value, 'g', -1, 64))
	s.openAIAdvancedQuotaMultiplierCache.Store(&cachedOpenAIAdvancedQuotaMultiplier{
		value:     value,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	})
}
