package service

import (
	"context"
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// InsightService serves the saved price-per-unit insights to the API: the
// dashboard's unread list and the per-row dismiss. It is a thin read layer —
// the findings themselves are written by the insight analysis job.
type InsightService struct {
	insights InsightStore
}

func NewInsightService(insights InsightStore) *InsightService {
	return &InsightService{insights: insights}
}

// List returns saved insights newest first. Unseen=true limits to the
// unread rows the dashboard shows; limit 0 uses the store's default.
func (s *InsightService) List(ctx context.Context, unseen bool, limit int) ([]domain.ProductInsight, error) {
	if limit < 0 {
		return nil, validationError("limit %d must be >= 0", limit)
	}
	return s.insights.List(ctx, domain.InsightFilters{Unseen: unseen, Limit: limit})
}

// Dismiss acknowledges one insight (removes it from the unread list).
func (s *InsightService) Dismiss(ctx context.Context, id int64) error {
	if id <= 0 {
		return validationError("id %d must be positive", id)
	}
	if err := s.insights.Dismiss(ctx, id); err != nil {
		return fmt.Errorf("dismiss insight %d: %w", id, err)
	}
	return nil
}
