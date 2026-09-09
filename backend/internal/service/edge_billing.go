package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (s *OpenAIGatewayService) TrackEdgeTurnState(c *gin.Context, account *Account) {
	s.noteOpenAICodexTurnStateProvenance(c, account)
}

// ApplyEdgeBill reuses the existing at-most-once transaction and idempotent log
// insert. The caller persists this exact command before calling and retries it
// unchanged after a crash or an ambiguous commit acknowledgement.
func (s *OpenAIGatewayService) ApplyEdgeBill(ctx context.Context, cmd *UsageBillingCommand, log *UsageLog) error {
	if cmd == nil || log == nil || !edgebridge.PilotUserAllowed(cmd.UserID) || log.UserID != cmd.UserID || cmd.AccountID != log.AccountID || cmd.RequestID != log.RequestID || cmd.APIKeyID != log.APIKeyID {
		return errors.New("invalid edge bill")
	}
	if _, err := s.usageBillingRepo.Apply(ctx, cmd); err != nil {
		return err
	}
	if _, err := s.usageLogRepo.Create(ctx, log); err != nil {
		return err
	}
	if s.billingCacheService != nil {
		if err := s.billingCacheService.InvalidateUserBalance(ctx, cmd.UserID); err != nil {
			return err
		}
	}
	return nil
}
func (s *OpenAIGatewayService) ObserveEdgeResponse(ctx context.Context, account *Account, status int, headers http.Header) {
	s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, headers)
	if status >= 400 && s.rateLimitService != nil {
		s.rateLimitService.HandleUpstreamError(ctx, account, status, headers, []byte(`{"error":{"type":"edge_upstream_error"}}`))
	}
}

func (s *OpenAIGatewayService) BindEdgeResponse(ctx context.Context, groupID, accountID int64, responseID string) error {
	if responseID == "" {
		return nil
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return errors.New("response affinity store unavailable")
	}
	return store.BindResponseAccount(ctx, groupID, responseID, accountID, s.openAIWSResponseStickyTTL())
}

func (s *OpenAIGatewayService) RecordEdgeOutcome(ctx context.Context, request string, key int64, v UsageOutcome) error {
	repo, ok := s.usageLogRepo.(UsageOutcomeRepository)
	if !ok {
		return errors.New("usage outcome store unavailable")
	}
	return repo.SetUsageOutcome(ctx, request, key, v)
}

func (s *ConcurrencyService) RefreshEdgeSlots(ctx context.Context, id string, userID, keyID, accountID int64, userMax, accountMax int) error {
	cache, ok := s.cache.(LiveConcurrencyCache)
	if !ok {
		return errors.New("live lease cache unavailable")
	}
	owned, err := cache.RefreshLiveLease(ctx, accountID, userID, keyID, "edge:"+id)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("edge concurrency lease lost")
	}
	return nil
}
func (s *ConcurrencyService) ReleaseEdgeSlots(ctx context.Context, id string, userID, keyID, accountID int64) error {
	cache, ok := s.cache.(LiveConcurrencyCache)
	if !ok {
		return errors.New("live lease cache unavailable")
	}
	return cache.ReleaseLiveLease(ctx, accountID, userID, keyID, "edge:"+id)
}
