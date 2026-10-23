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

type IngestionQueue interface {
	ClaimNextJob(context.Context, time.Duration) (domain.IngestionJob, error)
	CompleteJob(context.Context, uuid.UUID, uuid.UUID, []domain.DocumentChunk) error
	FailJob(context.Context, uuid.UUID, uuid.UUID, string) error
	ScheduleRetry(context.Context, uuid.UUID, uuid.UUID, string, time.Duration) error
}
type IngestionVersions interface {
	GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error)
}
type IngestClient interface {
	Ingest(context.Context, aiclient.Request) (aiclient.Response, error)
}
type WorkerConfig struct{ PollInterval, RequestTimeout time.Duration }
type IngestionWorker struct {
	queue    IngestionQueue
	versions IngestionVersions
	storage  DocumentStorage
	client   IngestClient
	log      *zap.Logger
	cfg      WorkerConfig
}

func NewIngestionWorker(q IngestionQueue, v IngestionVersions, s DocumentStorage, c IngestClient, log *zap.Logger, cfg WorkerConfig) (*IngestionWorker, error) {
	if q == nil || v == nil || s == nil || c == nil || log == nil || cfg.PollInterval < time.Millisecond || cfg.PollInterval > time.Hour || cfg.RequestTimeout < time.Second || cfg.RequestTimeout > time.Hour {
		return nil, errors.New("ingestion: invalid worker configuration")
	}
	return &IngestionWorker{q, v, s, c, log, cfg}, nil
}

// Run is safe to call concurrently. Claims fence all mutations in PostgreSQL.
func (w *IngestionWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := w.ProcessNext(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			w.log.Warn("ingestion iteration failed")
		}
		timer := time.NewTimer(w.cfg.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *IngestionWorker) ProcessNext(ctx context.Context) error {
	// The remote request and DB completion share a bounded operation context;
	// the lease has extra time for cancellation/transaction rollback.
	op, cancel := context.WithTimeout(ctx, w.cfg.RequestTimeout)
	defer cancel()
	job, err := w.queue.ClaimNextJob(op, w.cfg.RequestTimeout+30*time.Second)
	if err != nil {
		return err
	}
	token := job.LeaseToken.UUID
	fail := func(code string, temporary bool) error {
		if ctx.Err() != nil {
			return ctx.Err()
		} // Shutdown leaves recovery to lease expiry.
		cleanup, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		w.log.Warn("ingestion attempt failed", zap.String("job_id", job.ID.String()), zap.String("version_id", job.DocumentVersionID.String()), zap.String("code", code), zap.Int("attempt", job.Attempts))
		if temporary {
			return w.queue.ScheduleRetry(cleanup, job.ID, token, code, ingestionBackoff(job.Attempts))
		}
		return w.queue.FailJob(cleanup, job.ID, token, code)
	}
	version, err := w.versions.GetVersionByID(op, job.DocumentVersionID)
	if err != nil {
		return fail("processing_failed", !errors.Is(err, domain.ErrNotFound))
	}
	file, err := w.storage.Open(version.StorageKey)
	if err != nil {
		return fail("source_unavailable", false)
	}
	response, err := w.client.Ingest(op, aiclient.Request{DocumentID: version.DocumentID, DocumentVersionID: version.ID, MIMEType: version.MIMEType, Filename: version.OriginalFilename, File: file})
	file.Close()
	if err != nil {
		var remote *aiclient.Error
		if errors.As(err, &remote) {
			return fail(remote.Code, remote.Temporary)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fail("ai_timeout", true)
		}
		return fail("processing_failed", true)
	}
	if response.DocumentVersionID != version.ID || domain.ValidateChunks(response.Chunks) != nil {
		return fail("invalid_ai_response", false)
	}
	for _, c := range response.Chunks {
		if c.DocumentVersionID != uuid.Nil && c.DocumentVersionID != version.ID {
			return fail("invalid_ai_response", false)
		}
	}
	if err = w.queue.CompleteJob(op, job.ID, token, response.Chunks); err != nil {
		if errors.Is(err, domain.ErrLeaseLost) {
			return err
		}
		return fail("processing_failed", true)
	}
	w.log.Info("ingestion completed", zap.String("job_id", job.ID.String()), zap.String("version_id", version.ID.String()), zap.Int("chunk_count", len(response.Chunks)))
	return nil
}

func ingestionBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		return 5 * time.Minute
	}
	delay := 5 * time.Second * time.Duration(1<<uint(attempt-1))
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}
