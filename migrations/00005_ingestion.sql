-- +goose Up
CREATE TABLE ingestion_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_version_id UUID NOT NULL UNIQUE REFERENCES document_versions(id),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','completed','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMPTZ DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    lease_token UUID,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CHECK (max_attempts BETWEEN 1 AND 100 AND attempts BETWEEN 0 AND max_attempts),
    CHECK ((status='processing') = (lease_until IS NOT NULL AND lease_token IS NOT NULL)),
    CHECK (status='processing' OR (lease_until IS NULL AND lease_token IS NULL)),
    CHECK ((status='completed') = (completed_at IS NOT NULL))
);
CREATE INDEX ingestion_jobs_pending_idx ON ingestion_jobs (next_attempt_at,created_at,id) WHERE status='pending';
CREATE INDEX ingestion_jobs_lease_idx ON ingestion_jobs (lease_until,id) WHERE status='processing';

CREATE TABLE document_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_version_id UUID NOT NULL REFERENCES document_versions(id),
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
    content TEXT NOT NULL CHECK (length(btrim(content)) > 0),
    page_number INTEGER CHECK (page_number > 0),
    section_number TEXT,
    section_title TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_version_id,chunk_index)
);
-- The unique index above also indexes document_version_id as its leading column.
CREATE INDEX document_chunks_content_fts_idx ON document_chunks USING GIN (to_tsvector('russian'::regconfig,content));

-- +goose Down
DROP TABLE document_chunks;
DROP TABLE ingestion_jobs;
