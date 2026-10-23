package service

import (
	"context"
	"errors"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type IngestionJobs interface {
	GetJob(context.Context, uuid.UUID) (domain.IngestionJob, error)
	RetryFailedJob(context.Context, uuid.UUID, int) (domain.IngestionJob, error)
}
type IngestionService struct {
	jobs        IngestionJobs
	maxAttempts int
}

func NewIngestionService(jobs IngestionJobs, maxAttempts int) (*IngestionService, error) {
	if jobs == nil || maxAttempts < 1 || maxAttempts > 100 {
		return nil, errors.New("ingestion: invalid service configuration")
	}
	return &IngestionService{jobs: jobs, maxAttempts: maxAttempts}, nil
}
func (s *IngestionService) Get(ctx context.Context, actor domain.User, id uuid.UUID) (domain.IngestionJob, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.IngestionJob{}, err
	}
	return s.jobs.GetJob(ctx, id)
}
func (s *IngestionService) Retry(ctx context.Context, actor domain.User, id uuid.UUID) (domain.IngestionJob, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.IngestionJob{}, err
	}
	return s.jobs.RetryFailedJob(ctx, id, s.maxAttempts)
}
