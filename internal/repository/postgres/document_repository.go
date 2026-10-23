package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type DocumentRepository struct {
	db          *sql.DB
	maxAttempts int
}

func NewDocumentRepository(db *sql.DB) *DocumentRepository {
	return &DocumentRepository{db: db, maxAttempts: 5}
}
func NewDocumentRepositoryWithIngestion(db *sql.DB, maxAttempts int) (*DocumentRepository, error) {
	if maxAttempts < 1 || maxAttempts > 100 {
		return nil, fmt.Errorf("document repository: %w", domain.ErrInvalidInput)
	}
	return &DocumentRepository{db: db, maxAttempts: maxAttempts}, nil
}

const documentColumns = `id, title, created_by, created_at, updated_at, active_version_id`
const versionColumns = `id, document_id, version_number, storage_key, original_filename, mime_type, size_bytes, created_at, updated_at, status, sha256, (SELECT j.id FROM ingestion_jobs j WHERE j.document_version_id=document_versions.id) AS ingestion_job_id`

type scanner interface{ Scan(...any) error }

func scanDocument(row scanner) (domain.Document, error) {
	var d domain.Document
	err := row.Scan(&d.ID, &d.Title, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.ActiveVersionID)
	return d, mapError("read document", err)
}
func scanVersion(row scanner) (domain.DocumentVersion, error) {
	var v domain.DocumentVersion
	var checksum sql.NullString
	err := row.Scan(&v.ID, &v.DocumentID, &v.VersionNumber, &v.StorageKey, &v.OriginalFilename, &v.MIMEType, &v.SizeBytes, &v.CreatedAt, &v.UpdatedAt, &v.Status, &checksum, &v.IngestionJobID)
	v.SHA256 = checksum.String
	return v, mapError("read document version", err)
}

func (r *DocumentRepository) CreateDocumentWithVersion(ctx context.Context, doc domain.Document, version domain.DocumentVersion) (domain.Document, domain.DocumentVersion, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Document{}, domain.DocumentVersion{}, mapError("begin create document", err)
	}
	defer tx.Rollback()
	doc, err = scanDocument(tx.QueryRowContext(ctx, `INSERT INTO documents (title, created_by) VALUES ($1,$2) RETURNING `+documentColumns, doc.Title, doc.CreatedBy))
	if err != nil {
		return domain.Document{}, domain.DocumentVersion{}, err
	}
	version, err = scanVersion(tx.QueryRowContext(ctx, `INSERT INTO document_versions (document_id, version_number, storage_key, original_filename, mime_type, size_bytes, sha256) VALUES ($1,1,$2,$3,$4,$5,$6) RETURNING `+versionColumns, doc.ID, version.StorageKey, version.OriginalFilename, version.MIMEType, version.SizeBytes, nullableChecksum(version.SHA256)))
	if err != nil {
		return domain.Document{}, domain.DocumentVersion{}, err
	}
	doc, err = scanDocument(tx.QueryRowContext(ctx, `UPDATE documents SET active_version_id=$2 WHERE id=$1 RETURNING `+documentColumns, doc.ID, version.ID))
	if err != nil {
		return domain.Document{}, domain.DocumentVersion{}, err
	}
	if err := createJob(ctx, tx, version.ID, r.maxAttempts); err != nil {
		return domain.Document{}, domain.DocumentVersion{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ingestion_jobs WHERE document_version_id=$1`, version.ID).Scan(&version.IngestionJobID); err != nil {
		return domain.Document{}, domain.DocumentVersion{}, mapError("read new job ID", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Document{}, domain.DocumentVersion{}, mapError("commit create document", err)
	}
	return doc, version, nil
}

func nullableChecksum(checksum string) any {
	if checksum == "" {
		return nil
	} // Compatibility with legacy repository callers.
	return checksum
}

// lockDocument serializes number allocation and publication for one document.
func lockDocument(ctx context.Context, tx *sql.Tx, id uuid.UUID) error {
	var found uuid.UUID
	return mapError("lock document", tx.QueryRowContext(ctx, `SELECT id FROM documents WHERE id=$1 FOR UPDATE`, id).Scan(&found))
}

func (r *DocumentRepository) CreateVersion(ctx context.Context, documentID uuid.UUID, version domain.DocumentVersion) (domain.DocumentVersion, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DocumentVersion{}, mapError("begin create version", err)
	}
	defer tx.Rollback()
	if err := lockDocument(ctx, tx, documentID); err != nil {
		return domain.DocumentVersion{}, err
	}
	// Status, identity and sequence number are always assigned by the server.
	version, err = scanVersion(tx.QueryRowContext(ctx, `INSERT INTO document_versions (document_id,version_number,storage_key,original_filename,mime_type,size_bytes,sha256)
        SELECT $1,COALESCE(MAX(version_number),0)+1,$2,$3,$4,$5,$6 FROM document_versions WHERE document_id=$1
        RETURNING `+versionColumns, documentID, version.StorageKey, version.OriginalFilename, version.MIMEType, version.SizeBytes, nullableChecksum(version.SHA256)))
	if err != nil {
		return domain.DocumentVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET updated_at=NOW(),active_version_id=COALESCE(active_version_id,$2) WHERE id=$1`, documentID, version.ID); err != nil {
		return domain.DocumentVersion{}, mapError("update document version timestamp", err)
	}
	if err := createJob(ctx, tx, version.ID, r.maxAttempts); err != nil {
		return domain.DocumentVersion{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ingestion_jobs WHERE document_version_id=$1`, version.ID).Scan(&version.IngestionJobID); err != nil {
		return domain.DocumentVersion{}, mapError("read new job ID", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.DocumentVersion{}, mapError("commit create version", err)
	}
	return version, nil
}

func (r *DocumentRepository) ListVersions(ctx context.Context, documentID uuid.UUID, limit, offset int) ([]domain.DocumentVersion, int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, mapError("begin list versions", err)
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM document_versions WHERE document_id=$1`, documentID).Scan(&total); err != nil {
		return nil, 0, mapError("count versions", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+versionColumns+` FROM document_versions WHERE document_id=$1 ORDER BY version_number DESC LIMIT $2 OFFSET $3`, documentID, limit, offset)
	if err != nil {
		return nil, 0, mapError("list versions", err)
	}
	defer rows.Close()
	items := make([]domain.DocumentVersion, 0)
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, mapError("read versions", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, mapError("close versions", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, mapError("commit list versions", err)
	}
	return items, total, nil
}

func (r *DocumentRepository) GetActiveVersion(ctx context.Context, documentID uuid.UUID) (domain.DocumentVersion, error) {
	return scanVersion(r.db.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM document_versions WHERE document_id=$1 AND id=(SELECT active_version_id FROM documents WHERE id=$1)`, documentID))
}

// SetActiveVersion is internal publication. Locking the version also prevents a
// concurrent status update between the ready check and active-pointer update.
func (r *DocumentRepository) SetActiveVersion(ctx context.Context, documentID, versionID uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin publish version", err)
	}
	defer tx.Rollback()
	if err := lockDocument(ctx, tx, documentID); err != nil {
		return err
	}
	var status domain.VersionStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM document_versions WHERE document_id=$1 AND id=$2 FOR UPDATE`, documentID, versionID).Scan(&status); err != nil {
		return mapError("get publish version", err)
	}
	if status != domain.VersionReady {
		return domain.ErrInvalidInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET active_version_id=$2,updated_at=NOW() WHERE id=$1`, documentID, versionID); err != nil {
		return mapError("publish version", err)
	}
	return mapError("commit publish version", tx.Commit())
}

// UpdateVersionStatus changes processing metadata only, never original content.
func (r *DocumentRepository) UpdateVersionStatus(ctx context.Context, documentID, versionID uuid.UUID, status domain.VersionStatus) error {
	if !status.Valid() {
		return domain.ErrInvalidInput
	}
	result, err := r.db.ExecContext(ctx, `UPDATE document_versions SET status=$3,updated_at=NOW() WHERE document_id=$1 AND id=$2`, documentID, versionID, status)
	return requireRow("update version status", result, err)
}

func (r *DocumentRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.Document, error) {
	return scanDocument(r.db.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents WHERE id=$1`, id))
}
func (r *DocumentRepository) GetVersionByID(ctx context.Context, id uuid.UUID) (domain.DocumentVersion, error) {
	return scanVersion(r.db.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM document_versions WHERE id=$1`, id))
}
func (r *DocumentRepository) GetLatestVersion(ctx context.Context, documentID uuid.UUID) (domain.DocumentVersion, error) {
	return scanVersion(r.db.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM document_versions WHERE document_id=$1 ORDER BY version_number DESC LIMIT 1`, documentID))
}
func (r *DocumentRepository) ListAll(ctx context.Context, limit, offset int) ([]domain.Document, int64, error) {
	return r.list(ctx, uuid.Nil, false, limit, offset)
}
func (r *DocumentRepository) ListAccessibleByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Document, int64, error) {
	return r.list(ctx, userID, true, limit, offset)
}

func (r *DocumentRepository) list(ctx context.Context, userID uuid.UUID, filtered bool, limit, offset int) ([]domain.Document, int64, error) {
	// The access predicate is in both queries; pages and totals share a snapshot.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, mapError("begin list documents", err)
	}
	defer tx.Rollback()
	predicate := ` WHERE (NOT $1::boolean OR EXISTS (SELECT 1 FROM document_access a JOIN users u ON u.id=a.user_id WHERE a.document_id=documents.id AND a.user_id=$2 AND u.is_active))`
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM documents`+predicate, filtered, userID).Scan(&total); err != nil {
		return nil, 0, mapError("count documents", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+documentColumns+` FROM documents`+predicate+` ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, filtered, userID, limit, offset)
	if err != nil {
		return nil, 0, mapError("list documents", err)
	}
	defer rows.Close()
	items := make([]domain.Document, 0)
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, mapError("read documents", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, mapError("close documents", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, mapError("commit list documents", err)
	}
	return items, total, nil
}
