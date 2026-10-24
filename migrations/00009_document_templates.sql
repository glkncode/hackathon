-- +goose Up
CREATE TABLE document_templates (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 document_id UUID NOT NULL REFERENCES documents(id),
 document_version_id UUID NOT NULL,
 name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
 description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 fields JSONB NOT NULL CHECK (jsonb_typeof(fields) = 'array' AND jsonb_array_length(fields) BETWEEN 1 AND 64),
 schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 FOREIGN KEY (document_id, document_version_id) REFERENCES document_versions(document_id,id),
 UNIQUE (id, document_version_id)
);
CREATE INDEX document_templates_source_idx ON document_templates(document_id,document_version_id);
CREATE INDEX document_templates_catalog_idx ON document_templates(created_at DESC,id) WHERE enabled;
CREATE TABLE template_drafts (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 template_id UUID NOT NULL,
 document_version_id UUID NOT NULL,
 owner_id UUID NOT NULL REFERENCES users(id),
 values JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(values) = 'object' AND octet_length(values::text) <= 65536),
 status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','ready_to_generate')),
 revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 FOREIGN KEY (template_id,document_version_id) REFERENCES document_templates(id,document_version_id),
 UNIQUE (id,owner_id,template_id,document_version_id)
);
CREATE INDEX template_drafts_owner_idx ON template_drafts(owner_id,updated_at DESC,id);
CREATE INDEX template_drafts_template_idx ON template_drafts(template_id);
CREATE TABLE template_generated_files (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 draft_id UUID NOT NULL,
 owner_id UUID NOT NULL REFERENCES users(id),
 template_id UUID NOT NULL,
 document_version_id UUID NOT NULL,
 draft_revision BIGINT NOT NULL CHECK (draft_revision > 0),
 storage_key TEXT NOT NULL UNIQUE CHECK (storage_key ~ '^[0-9a-f-]{36}$'),
 filename TEXT NOT NULL CHECK (length(filename) BETWEEN 1 AND 255),
 mime_type TEXT NOT NULL CHECK (mime_type = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'),
 size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 FOREIGN KEY (draft_id,owner_id,template_id,document_version_id) REFERENCES template_drafts(id,owner_id,template_id,document_version_id),
 UNIQUE (draft_id,draft_revision)
);
CREATE INDEX template_generated_owner_idx ON template_generated_files(owner_id,created_at DESC,id);
CREATE INDEX template_generated_template_idx ON template_generated_files(template_id);

-- +goose Down
DROP TABLE template_generated_files;
DROP TABLE template_drafts;
DROP TABLE document_templates;
