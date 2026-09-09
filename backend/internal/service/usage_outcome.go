package service

import "context"

type UsageOutcome struct {
	Outcome       string `json:"outcome"`
	ErrorCode     string `json:"error_code,omitempty"`
	FirstOutputMS *int   `json:"first_output_ms,omitempty"`
}

type UsageOutcomeRepository interface {
	SetUsageOutcome(context.Context, string, int64, UsageOutcome) error
	GetUsageOutcomes(context.Context, []int64) (map[int64]UsageOutcome, error)
}

func (s *UsageService) GetUsageOutcomes(ctx context.Context, records []UsageLog) (map[int64]UsageOutcome, error) {
	repo, ok := s.usageRepo.(UsageOutcomeRepository)
	if !ok {
		return nil, nil
	}
	ids := make([]int64, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return repo.GetUsageOutcomes(ctx, ids)
}
