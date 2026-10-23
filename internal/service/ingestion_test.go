package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ingestionQueueStub struct {
	job                        domain.IngestionJob
	claimErr, completeErr      error
	completed, failed, retried int
	code                       string
	delay                      time.Duration
}

func (q *ingestionQueueStub) ClaimNextJob(context.Context, time.Duration) (domain.IngestionJob, error) {
	return q.job, q.claimErr
}
func (q *ingestionQueueStub) CompleteJob(_ context.Context, id, token uuid.UUID, chunks []domain.DocumentChunk) error {
	if id != q.job.ID || token != q.job.LeaseToken.UUID || chunks[0].DocumentVersionID != q.job.DocumentVersionID {
		return domain.ErrInvalidInput
	}
	q.completed++
	return q.completeErr
}
func (q *ingestionQueueStub) FailJob(_ context.Context, _, _ uuid.UUID, code string) error {
	q.failed++
	q.code = code
	return nil
}
func (q *ingestionQueueStub) ScheduleRetry(_ context.Context, _, _ uuid.UUID, code string, delay time.Duration) error {
	q.retried++
	q.code = code
	q.delay = delay
	return nil
}

type ingestionVersionStub struct{ version domain.DocumentVersion }

func (v ingestionVersionStub) GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return v.version, nil
}

type ingestFunc func(context.Context, aiclient.Request) (aiclient.Response, error)

func (f ingestFunc) Ingest(ctx context.Context, r aiclient.Request) (aiclient.Response, error) {
	return f(ctx, r)
}

func workerFixture(t *testing.T, client IngestClient) (*IngestionWorker, *ingestionQueueStub) {
	t.Helper()
	storage, err := NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	file, err := storage.Save(context.Background(), strings.NewReader("private text"), "rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	version := domain.DocumentVersion{ID: uuid.New(), DocumentID: uuid.New(), StorageKey: file.Key, OriginalFilename: file.Filename, MIMEType: file.MIMEType}
	q := &ingestionQueueStub{job: domain.IngestionJob{ID: uuid.New(), DocumentVersionID: version.ID, Attempts: 2, MaxAttempts: 5, LeaseToken: uuid.NullUUID{UUID: uuid.New(), Valid: true}}}
	worker, err := NewIngestionWorker(q, ingestionVersionStub{version}, storage, client, zap.NewNop(), WorkerConfig{PollInterval: time.Millisecond, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return worker, q
}
func TestWorkerProcessingOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		remote                     error
		invalid                    bool
		completeErr                error
		completed, failed, retried int
		code                       string
	}{
		{name: "success", completed: 1},
		{name: "Python unavailable", remote: &aiclient.Error{Code: "ai_unavailable", Temporary: true}, retried: 1, code: "ai_unavailable"},
		{name: "Python rejected", remote: &aiclient.Error{Code: "ai_rejected"}, failed: 1, code: "ai_rejected"},
		{name: "invalid chunks", invalid: true, failed: 1, code: "invalid_ai_response"},
		{name: "database rollback", completeErr: errors.New("db unavailable"), completed: 1, retried: 1, code: "processing_failed"},
		{name: "stale lease", completeErr: domain.ErrLeaseLost, completed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := ingestFunc(func(_ context.Context, r aiclient.Request) (aiclient.Response, error) {
				defer r.File.Close()
				chunks := []domain.DocumentChunk{{DocumentVersionID: r.DocumentVersionID, Content: "validated text"}}
				if tc.invalid {
					chunks[0].Content = ""
				}
				return aiclient.Response{DocumentVersionID: r.DocumentVersionID, Chunks: chunks}, tc.remote
			})
			w, q := workerFixture(t, client)
			q.completeErr = tc.completeErr
			err := w.ProcessNext(context.Background())
			if errors.Is(tc.completeErr, domain.ErrLeaseLost) {
				if !errors.Is(err, domain.ErrLeaseLost) {
					t.Fatal("stale completion not rejected")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if q.completed != tc.completed || q.failed != tc.failed || q.retried != tc.retried || q.code != tc.code {
				t.Fatalf("outcome: %+v", q)
			}
			if q.retried > 0 && q.delay != 10*time.Second {
				t.Fatal("backoff not applied")
			}
		})
	}
}
func TestWorkerGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	w, q := workerFixture(t, ingestFunc(func(ctx context.Context, r aiclient.Request) (aiclient.Response, error) {
		defer r.File.Close()
		close(started)
		<-ctx.Done()
		return aiclient.Response{}, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if q.completed != 0 || q.failed != 0 || q.retried != 0 {
		t.Fatal("shutdown mutated unfinished job instead of lease recovery")
	}
}
func TestWorkerIdleCancellationAndBackoffCap(t *testing.T) {
	w, q := workerFixture(t, ingestFunc(func(context.Context, aiclient.Request) (aiclient.Response, error) {
		t.Error("called AI on empty queue")
		return aiclient.Response{}, nil
	}))
	q.claimErr = domain.ErrNotFound
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)
	previous := time.Duration(0)
	for attempt := 1; attempt <= 100; attempt++ {
		delay := ingestionBackoff(attempt)
		if delay < previous || delay > 5*time.Minute {
			t.Fatal("unbounded backoff")
		}
		previous = delay
	}
}
