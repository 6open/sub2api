package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wei-Shaw/sub2api/internal/pkg/edgenode"
)

type edgeUsageEntry struct {
	Command *UsageBillingCommand
	Log     *UsageLog
}

func (s *OpenAIGatewayService) initializeEdgeOutbox() {
	if !edgenode.Enabled() {
		return
	}
	dir := os.Getenv("LKLB_EDGE_STATE_DIR")
	if !filepath.IsAbs(dir) || s.usageBillingRepo == nil || s.usageLogRepo == nil {
		panic("edge usage outbox is not configured")
	}
	box, err := edgenode.NewOutbox(filepath.Join(dir, "billing-outbox"), func(ctx context.Context, data []byte) error {
		var entry edgeUsageEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}
		if entry.Command == nil || entry.Log == nil || entry.Command.UserID != edgenode.AdminID ||
			entry.Log.UserID != entry.Command.UserID || entry.Log.APIKeyID != entry.Command.APIKeyID ||
			entry.Log.RequestID != entry.Command.RequestID {
			return errors.New("invalid edge billing entry")
		}
		if _, err := s.usageBillingRepo.Apply(ctx, entry.Command); err != nil {
			return err
		}
		if _, err := s.usageLogRepo.Create(ctx, entry.Log); err != nil {
			return err
		}
		if s.billingCacheService != nil {
			if err := s.billingCacheService.InvalidateUserBalance(ctx, entry.Command.UserID); err != nil {
				return err
			}
			if err := s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, entry.Command.APIKeyID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		panic("cannot initialize durable edge usage outbox")
	}
	s.edgeOutbox = box
}

func (s *OpenAIGatewayService) persistEdgeUsage(requestID string, log *UsageLog, p *postUsageBillingParams) error {
	cmd := buildUsageBillingCommand(requestID, log, p)
	if cmd == nil || s.edgeOutbox == nil {
		return errors.New("edge billing is unavailable")
	}
	cmd.Normalize()
	snapshot := *log
	snapshot.User = nil
	snapshot.APIKey = nil
	snapshot.Account = nil
	snapshot.Group = nil
	snapshot.Subscription = nil
	data, err := json.Marshal(edgeUsageEntry{Command: cmd, Log: &snapshot})
	if err != nil {
		return err
	}
	return s.edgeOutbox.Put(fmt.Sprintf("%d:%s", cmd.APIKeyID, cmd.RequestID), data)
}
