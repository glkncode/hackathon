package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

// Template definitions are immutable after registration; only availability changes.
type TemplateRepository struct{ db *sql.DB }

func NewTemplateRepository(db *sql.DB) *TemplateRepository { return &TemplateRepository{db: db} }

const templateColumns = `t.id,t.document_id,t.document_version_id,t.name,t.description,t.enabled,t.fields,t.schema_version,t.created_at,t.updated_at,t.profile`
const draftColumns = `d.id,d.template_id,d.document_version_id,d.owner_id,d.values,d.status,d.revision,t.fields,t.schema_version,d.created_at,d.updated_at,t.profile`
const generatedColumns = `id,draft_id,owner_id,template_id,document_version_id,draft_revision,storage_key,filename,mime_type,size_bytes,sha256,created_at,generation_snapshot`

// Resolve current user state and document ACL in the same query as pagination.
const templateVisibility = ` EXISTS (SELECT 1 FROM users u WHERE u.id=$1 AND u.is_active AND (u.role='admin' OR (u.role='member' AND EXISTS (SELECT 1 FROM document_access a WHERE a.document_id=t.document_id AND a.user_id=u.id)))) `

func scanTemplate(row scanner) (domain.DocumentTemplate, error) {
	var t domain.DocumentTemplate
	var data, profile []byte
	err := row.Scan(&t.ID, &t.DocumentID, &t.DocumentVersionID, &t.Name, &t.Description, &t.Enabled, &data, &t.SchemaVersion, &t.CreatedAt, &t.UpdatedAt, &profile)
	t.Profile = profile
	if err != nil {
		return t, mapError("read template", err)
	}
	if err = json.Unmarshal(data, &t.Fields); err != nil {
		return t, errors.New("invalid stored template schema")
	}
	return t, nil
}
func scanTemplateDraft(row scanner) (domain.TemplateDraft, error) {
	var d domain.TemplateDraft
	var values, fields, profile []byte
	err := row.Scan(&d.ID, &d.TemplateID, &d.DocumentVersionID, &d.OwnerID, &values, &d.Status, &d.Revision, &fields, &d.SchemaVersion, &d.CreatedAt, &d.UpdatedAt, &profile)
	d.Profile = profile
	if err != nil {
		return d, mapError("read template draft", err)
	}
	if json.Unmarshal(values, &d.Values) != nil || json.Unmarshal(fields, &d.Fields) != nil {
		return d, errors.New("invalid stored draft")
	}
	d.Progress()
	return d, nil
}
func scanGenerated(row scanner) (domain.GeneratedDocument, error) {
	var g domain.GeneratedDocument
	var snapshot []byte
	err := row.Scan(&g.ID, &g.DraftID, &g.OwnerID, &g.TemplateID, &g.DocumentVersionID, &g.DraftRevision, &g.StorageKey, &g.Filename, &g.MIMEType, &g.SizeBytes, &g.SHA256, &g.CreatedAt, &snapshot)
	g.GenerationSnapshot = snapshot
	return g, mapError("read generated document", err)
}
func (r *TemplateRepository) CreateTemplate(ctx context.Context, t domain.DocumentTemplate) (domain.DocumentTemplate, error) {
	fields, err := json.Marshal(t.Fields)
	if err != nil {
		return t, domain.ErrInvalidInput
	}
	version := t.SchemaVersion
	if version == 0 {
		version = 1
	}
	return scanTemplate(queryDB(ctx, r.db).QueryRowContext(ctx, `INSERT INTO document_templates AS t (document_id,document_version_id,name,description,fields,schema_version,profile) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+templateColumns, t.DocumentID, t.DocumentVersionID, t.Name, t.Description, fields, version, nullableJSON(t.Profile)))
}
func (r *TemplateRepository) GetTemplate(ctx context.Context, id uuid.UUID) (domain.DocumentTemplate, error) {
	return scanTemplate(queryDB(ctx, r.db).QueryRowContext(ctx, `SELECT `+templateColumns+` FROM document_templates t WHERE t.id=$1`, id))
}
func (r *TemplateRepository) SetTemplateEnabled(ctx context.Context, id uuid.UUID, enabled bool) (domain.DocumentTemplate, error) {
	return scanTemplate(queryDB(ctx, r.db).QueryRowContext(ctx, `UPDATE document_templates t SET enabled=$2,updated_at=NOW() WHERE id=$1 RETURNING `+templateColumns, id, enabled))
}
func (r *TemplateRepository) ListTemplates(ctx context.Context, owner uuid.UUID, limit, offset int) ([]domain.DocumentTemplate, error) {
	rows, err := queryDB(ctx, r.db).QueryContext(ctx, `SELECT `+templateColumns+` FROM document_templates t WHERE t.enabled AND `+templateVisibility+` ORDER BY t.created_at DESC,t.id LIMIT $2 OFFSET $3`, owner, limit, offset)
	if err != nil {
		return nil, mapError("list templates", err)
	}
	defer rows.Close()
	out := []domain.DocumentTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapError("list templates", rows.Err())
}
func (r *TemplateRepository) CreateDraft(ctx context.Context, owner uuid.UUID, t domain.DocumentTemplate) (domain.TemplateDraft, error) {
	id := uuid.New()
	_, err := queryDB(ctx, r.db).ExecContext(ctx, `INSERT INTO template_drafts(id,template_id,document_version_id,owner_id,status) SELECT $1,t.id,t.document_version_id,$2,$4 FROM document_templates t WHERE t.id=$3 AND t.enabled`, id, owner, t.ID, draftStatus(domain.ValidateTemplateValues(t.Fields, nil)))
	if err != nil {
		return domain.TemplateDraft{}, mapError("create template draft", err)
	}
	return r.GetDraft(ctx, owner, id)
}
func (r *TemplateRepository) GetDraft(ctx context.Context, owner, id uuid.UUID) (domain.TemplateDraft, error) {
	return scanTemplateDraft(queryDB(ctx, r.db).QueryRowContext(ctx, `SELECT `+draftColumns+` FROM template_drafts d JOIN document_templates t ON t.id=d.template_id WHERE d.id=$1 AND d.owner_id=$2`, id, owner))
}
func (r *TemplateRepository) ListDrafts(ctx context.Context, owner uuid.UUID, limit, offset int) ([]domain.TemplateDraft, error) {
	rows, err := queryDB(ctx, r.db).QueryContext(ctx, `SELECT `+draftColumns+` FROM template_drafts d JOIN document_templates t ON t.id=d.template_id WHERE d.owner_id=$1 AND t.enabled AND `+templateVisibility+` ORDER BY d.updated_at DESC,d.id LIMIT $2 OFFSET $3`, owner, limit, offset)
	if err != nil {
		return nil, mapError("list drafts", err)
	}
	defer rows.Close()
	out := []domain.TemplateDraft{}
	for rows.Next() {
		d, err := scanTemplateDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapError("list drafts", rows.Err())
}
func draftStatus(v domain.TemplateValidation) string {
	if v.Ready {
		return "ready_to_generate"
	}
	return "draft"
}
func (r *TemplateRepository) UpdateDraft(ctx context.Context, d domain.TemplateDraft, expected int64) (domain.TemplateDraft, error) {
	data, err := json.Marshal(d.Values)
	if err != nil {
		return d, domain.ErrInvalidInput
	}
	// The returned schema/version and revision belong to this exact CAS update.
	validation := domain.ValidateTemplateValues(d.Fields, d.Values)
	if d.Validation != nil {
		validation = *d.Validation
	}
	result, err := scanTemplateDraft(queryDB(ctx, r.db).QueryRowContext(ctx, `WITH changed AS (UPDATE template_drafts SET values=$4,status=$5,revision=revision+1,updated_at=NOW() WHERE id=$1 AND owner_id=$2 AND revision=$3 RETURNING *) SELECT `+draftColumns+` FROM changed d JOIN document_templates t ON t.id=d.template_id`, d.ID, d.OwnerID, expected, data, draftStatus(validation)))
	if errors.Is(err, domain.ErrNotFound) {
		if _, getErr := r.GetDraft(ctx, d.OwnerID, d.ID); getErr != nil {
			return result, getErr
		}
		return result, domain.ErrRevisionConflict
	}
	return result, err
}
func (r *TemplateRepository) GetGenerated(ctx context.Context, owner, id uuid.UUID) (domain.GeneratedDocument, error) {
	return scanGenerated(queryDB(ctx, r.db).QueryRowContext(ctx, `SELECT `+generatedColumns+` FROM template_generated_files WHERE id=$1 AND owner_id=$2`, id, owner))
}

// Generate holds one row lock, not a global lock. PostgreSQL serializes edits and
// retries; a crash rolls back the transaction and never exposes success metadata.
func (r *TemplateRepository) Generate(ctx context.Context, owner, id uuid.UUID, revision int64, build func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error)) (domain.GeneratedDocument, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.GeneratedDocument{}, mapError("begin generation", err)
	}
	defer tx.Rollback()
	var locked uuid.UUID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM template_drafts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&locked); err != nil {
		return domain.GeneratedDocument{}, mapError("lock draft", err)
	}
	d, err := scanTemplateDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM template_drafts d JOIN document_templates t ON t.id=d.template_id WHERE d.id=$1 AND d.owner_id=$2`, id, owner))
	if err != nil {
		return domain.GeneratedDocument{}, err
	}
	if d.Revision != revision {
		return domain.GeneratedDocument{}, domain.ErrRevisionConflict
	}
	previous, err := scanGenerated(tx.QueryRowContext(ctx, `SELECT `+generatedColumns+` FROM template_generated_files WHERE draft_id=$1 AND draft_revision=$2`, id, revision))
	if err == nil {
		return previous, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return previous, err
	}
	g, err := build(ctx, d)
	if err != nil {
		return g, err
	}
	g, err = scanGenerated(tx.QueryRowContext(ctx, `INSERT INTO template_generated_files (draft_id,owner_id,template_id,document_version_id,draft_revision,storage_key,filename,mime_type,size_bytes,sha256,generation_snapshot) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+generatedColumns, d.ID, d.OwnerID, d.TemplateID, d.DocumentVersionID, d.Revision, g.StorageKey, g.Filename, g.MIMEType, g.SizeBytes, g.SHA256, nullableJSON(g.GenerationSnapshot)))
	if err != nil {
		return g, err
	}
	if err = tx.Commit(); err != nil {
		return g, errors.Join(domain.ErrGenerationCommitUnknown, mapError("commit generation", err))
	}
	return g, nil
}

func nullableJSON(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}
