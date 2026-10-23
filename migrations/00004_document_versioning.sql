-- +goose Up
ALTER TABLE document_versions
    ADD COLUMN status TEXT NOT NULL DEFAULT 'uploaded',
    ADD COLUMN sha256 TEXT,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD CONSTRAINT document_versions_status_valid CHECK (status IN ('uploaded', 'processing', 'ready', 'failed')),
    ADD CONSTRAINT document_versions_sha256_valid CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT document_versions_document_id_id_unique UNIQUE (document_id, id);

-- Existing originals are on disk, inaccessible to a SQL migration. Their checksum
-- remains NULL until a trusted maintenance operation computes it from the file.
UPDATE document_versions SET updated_at = created_at;

ALTER TABLE documents ADD COLUMN active_version_id UUID;
-- Preserve the original API's choice of the latest existing file. These versions
-- remain uploaded: being downloadable does not imply successful indexing.
UPDATE documents d SET active_version_id = (
    SELECT v.id FROM document_versions v
    WHERE v.document_id = d.id ORDER BY v.version_number DESC LIMIT 1
);
ALTER TABLE documents ADD CONSTRAINT documents_active_version_same_document_fk
    FOREIGN KEY (id, active_version_id) REFERENCES document_versions (document_id, id);

-- Migration 3's UNIQUE(document_id, version_number) already provides the B-tree
-- index for history, next-number lookup and latest-version lookup. Do not duplicate it.

-- +goose Down
ALTER TABLE documents DROP CONSTRAINT documents_active_version_same_document_fk;
ALTER TABLE documents DROP COLUMN active_version_id;
ALTER TABLE document_versions
    DROP CONSTRAINT document_versions_document_id_id_unique,
    DROP CONSTRAINT document_versions_status_valid,
    DROP CONSTRAINT document_versions_sha256_valid,
    DROP COLUMN status,
    DROP COLUMN sha256,
    DROP COLUMN updated_at;
