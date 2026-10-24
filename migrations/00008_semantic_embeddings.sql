-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name='vector') THEN
        RAISE EXCEPTION 'pgvector is unavailable: use pgvector/pgvector:pg17-bookworm (PostgreSQL 17). Back up and verify the existing volume first; see docs/semantic-search.md. Never remove named volumes.';
    END IF;
END $$;
-- +goose StatementEnd
CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

ALTER TABLE document_chunks ADD CONSTRAINT document_chunks_id_version_unique UNIQUE (id, document_version_id);

CREATE TABLE semantic_indexes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_version_id UUID NOT NULL REFERENCES document_versions(id),
    model TEXT NOT NULL,
    revision TEXT NOT NULL,
    format_revision TEXT NOT NULL,
    dimensions INTEGER NOT NULL CHECK (dimensions=384),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','ready','failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (document_version_id, model, revision, format_revision),
    UNIQUE (id, document_version_id),
    CHECK ((status='processing') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (status='processing' OR (lease_token IS NULL AND lease_until IS NULL)),
    CHECK ((status='ready') = (completed_at IS NOT NULL))
);
CREATE INDEX semantic_indexes_queue ON semantic_indexes(next_attempt_at, created_at) WHERE status IN ('pending','processing');

-- One atomic manifest per fully saved chunk; progress survives process restarts.
CREATE TABLE semantic_chunks (
    index_id UUID NOT NULL,
    document_version_id UUID NOT NULL,
    chunk_id UUID NOT NULL,
    window_count INTEGER NOT NULL CHECK (window_count>0),
    total_tokens INTEGER NOT NULL CHECK (total_tokens>0),
    PRIMARY KEY (index_id, chunk_id),
    FOREIGN KEY (index_id, document_version_id) REFERENCES semantic_indexes(id,document_version_id),
    FOREIGN KEY (chunk_id, document_version_id) REFERENCES document_chunks(id,document_version_id)
);
CREATE TABLE embedding_windows (
    index_id UUID NOT NULL,
    document_version_id UUID NOT NULL,
    chunk_id UUID NOT NULL,
    window_index INTEGER NOT NULL CHECK (window_index>=0),
    start_token INTEGER NOT NULL CHECK (start_token>=0),
    end_token INTEGER NOT NULL,
    embedding public.vector(384) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (index_id,chunk_id,window_index),
    FOREIGN KEY (index_id,chunk_id) REFERENCES semantic_chunks(index_id,chunk_id) ON DELETE CASCADE,
    FOREIGN KEY (index_id,document_version_id) REFERENCES semantic_indexes(id,document_version_id),
    FOREIGN KEY (chunk_id,document_version_id) REFERENCES document_chunks(id,document_version_id),
    CHECK (end_token>start_token AND end_token-start_token<=508),
    CHECK (abs(public.vector_norm(embedding)-1)<0.001)
);

-- No ANN indexes: exact cosine scan with SQL visibility predicates.
-- +goose Down
DROP TABLE embedding_windows;
DROP TABLE semantic_chunks;
DROP TABLE semantic_indexes;
ALTER TABLE document_chunks DROP CONSTRAINT document_chunks_id_version_unique;
-- Keep the database-wide extension: other schemas may use it.
