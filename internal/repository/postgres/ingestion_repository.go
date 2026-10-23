package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type IngestionRepository struct{ db *sql.DB }

func NewIngestionRepository(db *sql.DB) *IngestionRepository { return &IngestionRepository{db: db} }

const jobColumns = `id,document_version_id,status,attempts,max_attempts,next_attempt_at,lease_until,lease_token,last_error,created_at,updated_at,completed_at`

func scanJob(row scanner) (domain.IngestionJob, error) {
	var j domain.IngestionJob
	var last sql.NullString
	err := row.Scan(&j.ID, &j.DocumentVersionID, &j.Status, &j.Attempts, &j.MaxAttempts, &j.NextAttemptAt, &j.LeaseUntil, &j.LeaseToken, &last, &j.CreatedAt, &j.UpdatedAt, &j.CompletedAt)
	j.LastError = safeJobError(last.String)
	return j, mapError("read ingestion job", err)
}

// Never persist or expose arbitrary error strings, response bodies or secrets.
func safeJobError(code string) string {
	switch code {
	case "ai_unavailable", "ai_timeout", "ai_rejected", "invalid_ai_response", "source_unavailable", "lease_expired", "processing_failed":
		return code
	case "":
		return ""
	default:
		return "processing_failed"
	}
}
func createJob(ctx context.Context, tx *sql.Tx, versionID uuid.UUID, maxAttempts int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ingestion_jobs (document_version_id,max_attempts) VALUES ($1,$2) ON CONFLICT (document_version_id) DO NOTHING`, versionID, maxAttempts)
	return mapError("create ingestion job", err)
}
func (r *IngestionRepository) CreateJob(ctx context.Context, versionID uuid.UUID, maxAttempts int) (domain.IngestionJob, error) {
	if maxAttempts < 1 || maxAttempts > 100 {
		return domain.IngestionJob{}, domain.ErrInvalidInput
	}
	// Return the same job on duplicate enqueue; never reset an existing lease.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.IngestionJob{}, mapError("begin enqueue", err)
	}
	defer tx.Rollback()
	if err := createJob(ctx, tx, versionID, maxAttempts); err != nil {
		return domain.IngestionJob{}, err
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE document_version_id=$1`, versionID))
	if err != nil {
		return domain.IngestionJob{}, err
	}
	return j, mapError("commit enqueue", tx.Commit())
}
func (r *IngestionRepository) GetJob(ctx context.Context, id uuid.UUID) (domain.IngestionJob, error) {
	return scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE id=$1`, id))
}
func (r *IngestionRepository) BackfillUploaded(ctx context.Context, maxAttempts int) (int64, error) {
	if maxAttempts < 1 || maxAttempts > 100 {
		return 0, domain.ErrInvalidInput
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO ingestion_jobs (document_version_id,max_attempts)
        SELECT id,$1 FROM document_versions WHERE status='uploaded' ON CONFLICT (document_version_id) DO NOTHING`, maxAttempts)
	if err != nil {
		return 0, mapError("enqueue existing uploaded versions", err)
	}
	count, err := result.RowsAffected()
	return count, mapError("count enqueued versions", err)
}

func (r *IngestionRepository) ClaimNextJob(ctx context.Context, lease time.Duration) (domain.IngestionJob, error) {
	if lease < time.Second || lease > 2*time.Hour {
		return domain.IngestionJob{}, domain.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.IngestionJob{}, mapError("begin claim job", err)
	}
	defer tx.Rollback()
	// A crashed final attempt cannot be retried again. Reap one per claim, without
	// blocking other processes; its version and job fail in the same transaction.
	exhausted, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE status='processing' AND lease_until<=clock_timestamp() AND attempts>=max_attempts ORDER BY lease_until,id FOR UPDATE SKIP LOCKED LIMIT 1`))
	if err == nil {
		if err := finishFailure(ctx, tx, exhausted, "lease_expired", true, 0, false); err != nil {
			return domain.IngestionJob{}, err
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.IngestionJob{}, err
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs
        WHERE attempts<max_attempts AND ((status='pending' AND (next_attempt_at IS NULL OR next_attempt_at<=clock_timestamp())) OR (status='processing' AND lease_until<=clock_timestamp()))
        ORDER BY COALESCE(next_attempt_at,lease_until,created_at),created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`))
	if errors.Is(err, domain.ErrNotFound) {
		if err := tx.Commit(); err != nil {
			return domain.IngestionJob{}, mapError("commit idle claim", err)
		}
		return domain.IngestionJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.IngestionJob{}, err
	}
	j, err = scanJob(tx.QueryRowContext(ctx, `UPDATE ingestion_jobs SET status='processing',attempts=attempts+1,
        lease_until=clock_timestamp()+$2*interval '1 second',lease_token=gen_random_uuid(),next_attempt_at=NULL,updated_at=clock_timestamp()
        WHERE id=$1 RETURNING `+jobColumns, j.ID, lease.Seconds()))
	if err != nil {
		return domain.IngestionJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE document_versions SET status='processing',updated_at=clock_timestamp() WHERE id=$1`, j.DocumentVersionID); err != nil {
		return domain.IngestionJob{}, mapError("mark version processing", err)
	}
	return j, mapError("commit claim job", tx.Commit())
}

func ownedJob(ctx context.Context, tx *sql.Tx, id, token uuid.UUID) (domain.IngestionJob, error) {
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE id=$1 AND status='processing' AND lease_token=$2 AND lease_until>clock_timestamp() FOR UPDATE`, id, token))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.IngestionJob{}, domain.ErrLeaseLost
	}
	return j, err
}

func (r *IngestionRepository) CompleteJob(ctx context.Context, id, token uuid.UUID, chunks []domain.DocumentChunk) error {
	if err := domain.ValidateChunks(chunks); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin complete job", err)
	}
	defer tx.Rollback()
	j, err := ownedJob(ctx, tx, id, token)
	if err != nil {
		return err
	}
	version, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM document_versions WHERE id=$1`, j.DocumentVersionID))
	if err != nil {
		return err
	}
	if err := lockDocument(ctx, tx, version.DocumentID); err != nil {
		return err
	}
	if err := replaceVersionChunks(ctx, tx, j.DocumentVersionID, chunks); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE document_versions SET status='ready',updated_at=clock_timestamp() WHERE id=$1`, j.DocumentVersionID); err != nil {
		return mapError("mark indexed version ready", err)
	}
	// Monotonic publication: an older result cannot replace a newer ready file.
	if _, err := tx.ExecContext(ctx, `UPDATE documents d SET active_version_id=$2,updated_at=clock_timestamp()
        WHERE d.id=$1 AND (d.active_version_id IS NULL OR NOT EXISTS
            (SELECT 1 FROM document_versions current WHERE current.id=d.active_version_id AND current.version_number>$3))`, version.DocumentID, version.ID, version.VersionNumber); err != nil {
		return mapError("publish indexed version", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE ingestion_jobs SET status='completed',completed_at=clock_timestamp(),updated_at=clock_timestamp(),lease_until=NULL,lease_token=NULL,last_error=NULL
        WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, id, token)
	if err != nil {
		return mapError("complete ingestion job", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return mapError("check completed lease", err)
	}
	if count != 1 {
		return domain.ErrLeaseLost
	}
	return mapError("commit complete job", tx.Commit())
}

func finishFailure(ctx context.Context, tx *sql.Tx, j domain.IngestionJob, code string, terminal bool, delay time.Duration, checkLease bool) error {
	jobStatus, versionStatus := domain.JobPending, domain.VersionUploaded
	if terminal || j.Attempts >= j.MaxAttempts {
		jobStatus, versionStatus = domain.JobFailed, domain.VersionFailed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE document_versions SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, j.DocumentVersionID, versionStatus); err != nil {
		return mapError("record version failure", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE ingestion_jobs SET status=$2::varchar,last_error=$3,lease_until=NULL,lease_token=NULL,next_attempt_at=CASE WHEN $2::varchar='pending' THEN clock_timestamp()+$4*interval '1 second' ELSE NULL END,updated_at=clock_timestamp() WHERE id=$1 AND (NOT $5::boolean OR (lease_token=$6 AND lease_until>clock_timestamp()))`, j.ID, jobStatus, safeJobError(code), delay.Seconds(), checkLease, j.LeaseToken)
	if err != nil {
		return mapError("record ingestion failure", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return mapError("check failed lease", err)
	}
	if count != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
func (r *IngestionRepository) failure(ctx context.Context, id, token uuid.UUID, code string, terminal bool, delay time.Duration) error {
	if delay < 0 || delay > time.Hour {
		return domain.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin job failure", err)
	}
	defer tx.Rollback()
	j, err := ownedJob(ctx, tx, id, token)
	if err != nil {
		return err
	}
	if err := finishFailure(ctx, tx, j, code, terminal, delay, true); err != nil {
		return err
	}
	return mapError("commit job failure", tx.Commit())
}
func (r *IngestionRepository) FailJob(ctx context.Context, id, token uuid.UUID, code string) error {
	return r.failure(ctx, id, token, code, true, 0)
}
func (r *IngestionRepository) ScheduleRetry(ctx context.Context, id, token uuid.UUID, code string, delay time.Duration) error {
	return r.failure(ctx, id, token, code, false, delay)
}

func (r *IngestionRepository) RetryFailedJob(ctx context.Context, id uuid.UUID, maxAttempts int) (domain.IngestionJob, error) {
	if maxAttempts < 1 || maxAttempts > 100 {
		return domain.IngestionJob{}, domain.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.IngestionJob{}, mapError("begin retry job", err)
	}
	defer tx.Rollback()
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return domain.IngestionJob{}, err
	}
	if j.Status != domain.JobFailed {
		return domain.IngestionJob{}, domain.ErrInvalidState
	}
	j, err = scanJob(tx.QueryRowContext(ctx, `UPDATE ingestion_jobs SET status='pending',attempts=0,max_attempts=$2,next_attempt_at=clock_timestamp(),last_error=NULL,completed_at=NULL,lease_until=NULL,lease_token=NULL,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+jobColumns, id, maxAttempts))
	if err != nil {
		return domain.IngestionJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE document_versions SET status='uploaded',updated_at=clock_timestamp() WHERE id=$1`, j.DocumentVersionID); err != nil {
		return domain.IngestionJob{}, mapError("reset version for retry", err)
	}
	return j, mapError("commit retry job", tx.Commit())
}
