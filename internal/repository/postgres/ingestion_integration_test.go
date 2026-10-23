package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func ingestionFixture(t *testing.T, n int, maxAttempts int) (*sql.DB, context.Context, *DocumentRepository, *IngestionRepository, domain.Document, []domain.DocumentVersion) {
	t.Helper()
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "ingestion-owner")
	docs, err := NewDocumentRepositoryWithIngestion(db, maxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	input := func() domain.DocumentVersion {
		return domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "rules.txt", MIMEType: "text/plain", SizeBytes: 10}
	}
	doc, v, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Rules", CreatedBy: user.ID}, input())
	if err != nil {
		t.Fatal(err)
	}
	versions := []domain.DocumentVersion{v}
	for i := 1; i < n; i++ {
		v, err = docs.CreateVersion(ctx, doc.ID, input())
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	return db, ctx, docs, NewIngestionRepository(db), doc, versions
}
func chunksFor(v uuid.UUID, text string) []domain.DocumentChunk {
	return []domain.DocumentChunk{{DocumentVersionID: v, ChunkIndex: 0, Content: text}}
}
func mustClaim(t *testing.T, ctx context.Context, r *IngestionRepository) domain.IngestionJob {
	t.Helper()
	j, err := r.ClaimNextJob(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func expire(t *testing.T, ctx context.Context, db *sql.DB, id uuid.UUID) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE ingestion_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationIngestionEnqueueAndBackfill(t *testing.T) {
	db, ctx, _, jobs, _, versions := ingestionFixture(t, 2, 3)
	for _, v := range versions {
		j, err := jobs.CreateJob(ctx, v.ID, 9)
		if err != nil || j.Status != domain.JobPending || j.MaxAttempts != 3 || !v.IngestionJobID.Valid || j.ID != v.IngestionJobID.UUID {
			t.Fatalf("enqueue: %+v %v", j, err)
		}
		again, err := jobs.CreateJob(ctx, v.ID, 9)
		if err != nil || again.ID != j.ID {
			t.Fatal("duplicate enqueue")
		}
	}
	if count, err := jobs.BackfillUploaded(ctx, 3); err != nil || count != 0 {
		t.Fatalf("backfill duplicates: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM ingestion_jobs WHERE document_version_id=$1`, versions[1].ID); err != nil {
		t.Fatal(err)
	}
	if count, err := jobs.BackfillUploaded(ctx, 3); err != nil || count != 1 {
		t.Fatalf("backfill missing: %d %v", count, err)
	}
	if count, err := jobs.BackfillUploaded(ctx, 3); err != nil || count != 0 {
		t.Fatal("non-idempotent backfill")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ingestion_jobs`).Scan(&count); err != nil || count != 2 {
		t.Fatal("not one job per version")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var index string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname='document_chunks_content_fts_idx'`).Scan(&index); err != nil {
		t.Fatal("missing FTS index", err)
	}
	var found bool
	if err := db.QueryRowContext(ctx, `SELECT to_tsvector('russian','согласования') @@ plainto_tsquery('russian','согласование')`).Scan(&found); err != nil || !found {
		t.Fatal("Russian stemming unavailable")
	}
}

func TestIntegrationConcurrentClaimsAndSkipLocked(t *testing.T) {
	db, ctx, _, jobs, _, versions := ingestionFixture(t, 8, 3)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM ingestion_jobs WHERE document_version_id=$1 FOR UPDATE`, versions[0].ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		j   domain.IngestionJob
		err error
	}
	results := make(chan result, 7)
	var wg sync.WaitGroup
	for range 7 {
		wg.Go(func() { j, err := jobs.ClaimNextJob(ctx, time.Minute); results <- result{j, err} })
	}
	wg.Wait()
	close(results)
	seen := map[uuid.UUID]bool{}
	for res := range results {
		if res.err != nil || res.j.DocumentVersionID == versions[0].ID || seen[res.j.ID] || res.j.Attempts != 1 || !res.j.LeaseToken.Valid {
			t.Fatalf("unsafe claim: %+v %v", res.j, res.err)
		}
		seen[res.j.ID] = true
	}
	if len(seen) != 7 {
		t.Fatal("claim count")
	}
	if _, err := jobs.ClaimNextJob(ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("claimed locked or leased job", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if j := mustClaim(t, ctx, jobs); j.DocumentVersionID != versions[0].ID {
		t.Fatal("locked job lost")
	}
}

func TestIntegrationLeaseRecoveryAndFencing(t *testing.T) {
	db, ctx, docs, jobs, _, versions := ingestionFixture(t, 1, 3)
	old := mustClaim(t, ctx, jobs)
	expire(t, ctx, db, old.ID)
	if err := jobs.CompleteJob(ctx, old.ID, old.LeaseToken.UUID, chunksFor(versions[0].ID, "stale")); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("expired completion accepted", err)
	}
	current := mustClaim(t, ctx, jobs)
	if current.ID != old.ID || current.Attempts != 2 || current.LeaseToken.UUID == old.LeaseToken.UUID {
		t.Fatal("lease not renewed safely")
	}
	for _, err := range []error{
		jobs.CompleteJob(ctx, old.ID, old.LeaseToken.UUID, chunksFor(versions[0].ID, "stale")),
		jobs.FailJob(ctx, old.ID, old.LeaseToken.UUID, "processing_failed"),
		jobs.ScheduleRetry(ctx, old.ID, old.LeaseToken.UUID, "ai_unavailable", 0),
	} {
		if !errors.Is(err, domain.ErrLeaseLost) {
			t.Fatal("stale worker mutated job", err)
		}
	}
	if err := jobs.CompleteJob(ctx, current.ID, current.LeaseToken.UUID, chunksFor(versions[0].ID, "Верный результат")); err != nil {
		t.Fatal(err)
	}
	v, err := docs.GetVersionByID(ctx, versions[0].ID)
	if err != nil || v.Status != domain.VersionReady {
		t.Fatal("ready status missing")
	}
	persisted, err := NewChunkRepository(db).GetChunksByVersion(ctx, v.ID)
	if err != nil || len(persisted) != 1 || persisted[0].Content != "Верный результат" {
		t.Fatal("stale chunks persisted")
	}
	j, err := jobs.GetJob(ctx, current.ID)
	if err != nil || j.Status != domain.JobCompleted || j.CompletedAt == nil || j.LeaseToken.Valid || j.LeaseUntil != nil {
		t.Fatal("completion metadata inconsistent")
	}
	if _, err := jobs.RetryFailedJob(ctx, j.ID, 3); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatal("retried completed job")
	}
}

func TestIntegrationRetryExhaustionAndCrashRecovery(t *testing.T) {
	db, ctx, docs, jobs, _, versions := ingestionFixture(t, 1, 2)
	first := mustClaim(t, ctx, jobs)
	if err := jobs.ScheduleRetry(ctx, first.ID, first.LeaseToken.UUID, "secret unsafe remote body", time.Hour); err != nil {
		t.Fatal(err)
	}
	j, _ := jobs.GetJob(ctx, first.ID)
	v, _ := docs.GetVersionByID(ctx, versions[0].ID)
	if j.Status != domain.JobPending || j.LastError != "processing_failed" || v.Status != domain.VersionUploaded {
		t.Fatal("retry metadata unsafe")
	}
	if _, err := jobs.ClaimNextJob(ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("backoff ignored")
	}
	if _, err := db.ExecContext(ctx, `UPDATE ingestion_jobs SET next_attempt_at=clock_timestamp() WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	second := mustClaim(t, ctx, jobs)
	if second.Attempts != 2 {
		t.Fatal("attempt not incremented")
	}
	if err := jobs.ScheduleRetry(ctx, second.ID, second.LeaseToken.UUID, "ai_unavailable", 0); err != nil {
		t.Fatal(err)
	}
	j, _ = jobs.GetJob(ctx, j.ID)
	v, _ = docs.GetVersionByID(ctx, v.ID)
	if j.Status != domain.JobFailed || v.Status != domain.VersionFailed || j.Attempts != 2 {
		t.Fatal("exhaustion did not fail atomically")
	}
	if _, err := jobs.ClaimNextJob(ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("exhausted job reclaimed")
	}
	reset, err := jobs.RetryFailedJob(ctx, j.ID, 1)
	if err != nil || reset.ID != j.ID || reset.Attempts != 0 || reset.LastError != "" || reset.Status != domain.JobPending {
		t.Fatal("manual reset failed", err)
	}
	if _, err := jobs.RetryFailedJob(ctx, j.ID, 1); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatal("reset pending job")
	}
	crashed := mustClaim(t, ctx, jobs)
	expire(t, ctx, db, crashed.ID)
	if _, err := jobs.ClaimNextJob(ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("crashed final attempt reclaimed")
	}
	j, _ = jobs.GetJob(ctx, j.ID)
	v, _ = docs.GetVersionByID(ctx, v.ID)
	if j.Status != domain.JobFailed || j.LastError != "lease_expired" || v.Status != domain.VersionFailed {
		t.Fatal("crashed final attempt left processing")
	}
}

func TestIntegrationAtomicChunksRollbackAndPublication(t *testing.T) {
	db, ctx, docs, jobs, doc, versions := ingestionFixture(t, 2, 3)
	chunkRepo := NewChunkRepository(db)
	old, newer := mustClaim(t, ctx, jobs), mustClaim(t, ctx, jobs)
	if old.DocumentVersionID != versions[0].ID {
		old, newer = newer, old
	}
	if err := chunkRepo.ReplaceVersionChunks(ctx, old.DocumentVersionID, chunksFor(old.DocumentVersionID, "previous")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_chunk() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.content='reject' THEN RAISE EXCEPTION 'test insertion failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_chunk BEFORE INSERT ON document_chunks FOR EACH ROW EXECUTE FUNCTION reject_chunk()`); err != nil {
		t.Fatal(err)
	}
	invalid := []domain.DocumentChunk{{ChunkIndex: 0, Content: "first"}, {ChunkIndex: 1, Content: "reject"}}
	if err := jobs.CompleteJob(ctx, old.ID, old.LeaseToken.UUID, invalid); err == nil {
		t.Fatal("insertion failure ignored")
	}
	persisted, _ := chunkRepo.GetChunksByVersion(ctx, old.DocumentVersionID)
	v, _ := docs.GetVersionByID(ctx, old.DocumentVersionID)
	j, _ := jobs.GetJob(ctx, old.ID)
	if len(persisted) != 1 || persisted[0].Content != "previous" || v.Status != domain.VersionProcessing || j.Status != domain.JobProcessing {
		t.Fatal("failed transaction partially committed")
	}
	mismatch := chunksFor(newer.DocumentVersionID, "wrong version")
	if err := jobs.CompleteJob(ctx, old.ID, old.LeaseToken.UUID, mismatch); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("cross-version chunks accepted")
	}
	if err := jobs.CompleteJob(ctx, newer.ID, newer.LeaseToken.UUID, chunksFor(newer.DocumentVersionID, "new")); err != nil {
		t.Fatal(err)
	}
	if err := jobs.CompleteJob(ctx, old.ID, old.LeaseToken.UUID, chunksFor(old.DocumentVersionID, "old")); err != nil {
		t.Fatal(err)
	}
	active, err := docs.GetActiveVersion(ctx, doc.ID)
	if err != nil || active.ID != newer.DocumentVersionID || active.Status != domain.VersionReady {
		t.Fatal("old completion overwrote new publication", err)
	}
	for _, v := range versions {
		if count, err := chunkRepo.CountChunksByVersion(ctx, v.ID); err != nil || count != 1 {
			t.Fatal("chunks missing")
		}
	}
}

func TestIntegrationJobFailureRollsBackVersion(t *testing.T) {
	db, ctx, docs, _, doc, _ := ingestionFixture(t, 1, 3)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test job insertion failure'; END $$;
 CREATE TRIGGER reject_job BEFORE INSERT ON ingestion_jobs FOR EACH ROW EXECUTE FUNCTION reject_job()`); err != nil {
		t.Fatal(err)
	}
	input := domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "new.txt", MIMEType: "text/plain", SizeBytes: 1}
	if _, err := docs.CreateVersion(ctx, doc.ID, input); err == nil {
		t.Fatal("job failure ignored")
	}
	if _, count, err := docs.ListVersions(ctx, doc.ID, 20, 0); err != nil || count != 1 {
		t.Fatal("orphan version committed")
	}
	user := createTestUser(t, ctx, db, "other-owner")
	if _, _, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Rollback", CreatedBy: user.ID}, input); err == nil {
		t.Fatal("job failure ignored on initial upload")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM documents`).Scan(&count); err != nil || count != 1 {
		t.Fatal("orphan document committed")
	}
}

func TestIntegrationLeaseExpiryDuringCompletionRollsBack(t *testing.T) {
	db, ctx, docs, jobs, _, versions := ingestionFixture(t, 1, 3)
	job := mustClaim(t, ctx, jobs)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION slow_chunk() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.15); RETURN NEW; END $$; CREATE TRIGGER slow_chunk BEFORE INSERT ON document_chunks FOR EACH ROW EXECUTE FUNCTION slow_chunk()`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ingestion_jobs SET lease_until=clock_timestamp()+interval '0.1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jobs.CompleteJob(ctx, job.ID, job.LeaseToken.UUID, chunksFor(versions[0].ID, "too late")); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("completion after expiry accepted", err)
	}
	if count, err := NewChunkRepository(db).CountChunksByVersion(ctx, versions[0].ID); err != nil || count != 0 {
		t.Fatal("expired transaction retained chunks")
	}
	v, err := docs.GetVersionByID(ctx, versions[0].ID)
	if err != nil || v.Status != domain.VersionProcessing {
		t.Fatal("expired transaction marked ready")
	}
}

func TestIntegrationFailedRetryPreservesChunksAndConstraints(t *testing.T) {
	db, ctx, _, jobs, _, versions := ingestionFixture(t, 1, 3)
	v := versions[0]
	chunks := NewChunkRepository(db)
	if err := chunks.ReplaceVersionChunks(ctx, v.ID, chunksFor(v.ID, "previous good result")); err != nil {
		t.Fatal(err)
	}
	job := mustClaim(t, ctx, jobs)
	if err := jobs.FailJob(ctx, job.ID, job.LeaseToken.UUID, "invalid_ai_response"); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.RetryFailedJob(ctx, job.ID, 3); err != nil {
		t.Fatal(err)
	}
	saved, err := chunks.GetChunksByVersion(ctx, v.ID)
	if err != nil || len(saved) != 1 || saved[0].Content != "previous good result" {
		t.Fatal("retry destroyed old chunks")
	}
	for _, query := range []string{
		`UPDATE ingestion_jobs SET status='unknown'`,
		`UPDATE ingestion_jobs SET attempts=-1`,
		`UPDATE ingestion_jobs SET attempts=max_attempts+1`,
		`UPDATE ingestion_jobs SET max_attempts=0`,
		`UPDATE ingestion_jobs SET status='processing'`,
		`UPDATE document_chunks SET page_number=0`,
		`UPDATE document_chunks SET content='  '`,
		`UPDATE document_chunks SET chunk_index=-1`,
	} {
		if _, err := db.ExecContext(ctx, query); !errors.Is(mapError("test constraint", err), domain.ErrInvalidInput) {
			t.Fatal("constraint not enforced", query, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO document_chunks (document_version_id,chunk_index,content) VALUES ($1,0,'duplicate')`, v.ID); !errors.Is(mapError("test duplicate", err), domain.ErrAlreadyExists) {
		t.Fatal("unique chunk index missing", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO document_chunks (document_version_id,chunk_index,content) VALUES ($1,0,'orphan')`, uuid.New()); !errors.Is(mapError("test FK", err), domain.ErrInvalidInput) {
		t.Fatal("chunk version FK missing", err)
	}
}

func TestIntegrationIngestionMigrationDownPreservesOriginals(t *testing.T) {
	db, ctx, _, jobs, doc, versions := ingestionFixture(t, 1, 3)
	job := mustClaim(t, ctx, jobs)
	if err := jobs.CompleteJob(ctx, job.ID, job.LeaseToken.UUID, chunksFor(versions[0].ID, "Правила")); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Down(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ingestion_jobs", "document_chunks"} {
		var name sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1)::text`, table).Scan(&name); err != nil || name.Valid {
			t.Fatal("ingestion Down retained table", table, err)
		}
	}
	var active uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT active_version_id FROM documents WHERE id=$1`, doc.ID).Scan(&active); err != nil || active != versions[0].ID {
		t.Fatal("ingestion Down modified document")
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM document_versions WHERE id=$1`, versions[0].ID).Scan(&status); err != nil || status != "ready" {
		t.Fatal("ingestion Down modified original metadata")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
}
