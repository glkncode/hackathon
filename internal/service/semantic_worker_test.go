package service

import (
	"ai-assistant/internal/domain"
	"context"
	"errors"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
	"time"
)

type semanticQueueStub struct {
	job                       domain.SemanticJob
	chunks                    []domain.DocumentChunk
	saved, completed, retried int
	err                       error
}

func (q *semanticQueueStub) EnqueueReady(context.Context) error { return nil }
func (q *semanticQueueStub) Claim(context.Context, time.Duration) (domain.SemanticJob, error) {
	return q.job, q.err
}
func (q *semanticQueueStub) PendingChunks(context.Context, domain.SemanticJob) ([]domain.DocumentChunk, error) {
	if q.saved > 0 {
		return nil, nil
	}
	return q.chunks, nil
}
func (q *semanticQueueStub) SaveChunk(context.Context, domain.SemanticJob, uuid.UUID, []domain.EmbeddingWindow, [][]float64, time.Duration) error {
	q.saved++
	return nil
}
func (q *semanticQueueStub) Complete(context.Context, domain.SemanticJob) error {
	q.completed++
	return nil
}
func (q *semanticQueueStub) Retry(context.Context, domain.SemanticJob, string, time.Duration) error {
	q.retried++
	return nil
}
func TestSemanticWorkerCompletesOnlyValidatedSaves(t *testing.T) {
	for _, mode := range []string{"ok", "offline", "invalid", "canceled", "unclaimed"} {
		t.Run(mode, func(t *testing.T) {
			q := &semanticQueueStub{job: domain.SemanticJob{ID: uuid.New(), VersionID: uuid.New(), LeaseToken: uuid.New()}, chunks: []domain.DocumentChunk{{ID: uuid.New(), Content: "новый документ"}}}
			c := &semanticStub{response: goodEmbedding()}
			switch mode {
			case "offline":
				c.embedErr = errors.New("offline")
			case "invalid":
				c.response.Revision = "wrong"
			case "unclaimed":
				q.err = domain.ErrNotFound
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err := NewSemanticWorker(q, c, zap.NewNop(), time.Second).ProcessNext(ctx)
			if mode == "ok" {
				if err != nil || q.saved != 1 || q.completed != 1 || q.retried != 0 {
					t.Fatal("completion", err)
				}
			} else {
				if err == nil || q.completed != 0 || q.saved != 0 {
					t.Fatal("failed attempt published index", err)
				}
				if (mode == "offline" || mode == "invalid") && q.retried != 1 {
					t.Fatal("no retry")
				}
			}
		})
	}
}
