package service

import (
	"context"
	"errors"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SemanticQueue interface {
	EnqueueReady(context.Context) error
	Claim(context.Context, time.Duration) (domain.SemanticJob, error)
	PendingChunks(context.Context, domain.SemanticJob) ([]domain.DocumentChunk, error)
	SaveChunk(context.Context, domain.SemanticJob, uuid.UUID, []domain.EmbeddingWindow, [][]float64, time.Duration) error
	Complete(context.Context, domain.SemanticJob) error
	Retry(context.Context, domain.SemanticJob, string, time.Duration) error
}
type EmbeddingClient interface {
	Embed(context.Context, string, []string) (aiclient.EmbeddingResponse, error)
}
type SemanticWorker struct {
	queue   SemanticQueue
	client  EmbeddingClient
	log     *zap.Logger
	timeout time.Duration
}

func NewSemanticWorker(q SemanticQueue, c EmbeddingClient, log *zap.Logger, timeout time.Duration) *SemanticWorker {
	return &SemanticWorker{q, c, log, timeout}
}
func (w *SemanticWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := w.ProcessNext(ctx)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, domain.ErrNotFound) {
			w.log.Warn("semantic indexing deferred", zap.String("reason", "indexing_unavailable"))
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (w *SemanticWorker) ProcessNext(ctx context.Context) error {
	op, cancel := context.WithTimeout(ctx, max(2*time.Minute, 2*w.timeout))
	defer cancel()
	if err := w.queue.EnqueueReady(op); err != nil {
		return err
	}
	lease := w.timeout + 30*time.Second
	j, err := w.queue.Claim(op, lease)
	if err != nil {
		return err
	}
	fail := func(code string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		w.log.Warn("semantic indexing retry", zap.String("index_id", j.ID.String()), zap.String("reason", code), zap.Int("attempt", j.Attempts))
		if err := w.queue.Retry(cleanup, j, code, ingestionBackoff(j.Attempts)); err != nil {
			return err
		}
		return errors.New("semantic indexing retry scheduled")
	}
	for ctx.Err() == nil {
		chunks, err := w.queue.PendingChunks(op, j)
		if err != nil {
			return fail("storage_unavailable")
		}
		if len(chunks) == 0 {
			if err = w.queue.Complete(op, j); err != nil {
				return err
			}
			w.log.Info("semantic index ready", zap.String("index_id", j.ID.String()), zap.String("version_id", j.VersionID.String()))
			return nil
		}
		for _, c := range chunks {
			response, err := w.client.Embed(op, "passage", []string{c.Content})
			if err != nil {
				return fail("embedding_unavailable")
			}
			if response.Validate("passage", 1) != nil {
				return fail("invalid_embedding_response")
			}
			if err = w.queue.SaveChunk(op, j, c.ID, response.Windows, response.Embeddings, lease); err != nil {
				if errors.Is(err, domain.ErrLeaseLost) {
					return err
				}
				return fail("storage_unavailable")
			}
		}
	}
	return ctx.Err()
}
