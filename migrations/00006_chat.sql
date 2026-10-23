-- +goose Up
CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ
);
CREATE INDEX chat_sessions_user_updated_idx ON chat_sessions(user_id,updated_at DESC,id);

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES chat_sessions(id),
    role VARCHAR(16) NOT NULL CHECK (role IN ('user','assistant')),
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 500),
    mode VARCHAR(16) CHECK (mode IN ('search','no_sources')),
    client_message_id UUID,
    reply_to_id UUID,
    position BIGINT GENERATED ALWAYS AS IDENTITY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id,id),
    UNIQUE (session_id,client_message_id),
    UNIQUE (reply_to_id),
    FOREIGN KEY (session_id,reply_to_id) REFERENCES chat_messages(session_id,id),
    CHECK ((role='user' AND mode IS NULL AND client_message_id IS NOT NULL AND reply_to_id IS NULL)
        OR (role='assistant' AND mode IS NOT NULL AND client_message_id IS NULL AND reply_to_id IS NOT NULL))
);
CREATE INDEX chat_messages_session_position_idx ON chat_messages(session_id,position);

CREATE TABLE chat_message_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID NOT NULL REFERENCES chat_messages(id),
    -- RESTRICT deliberately preserves cited chunks; reindex as a new version.
    chunk_id UUID NOT NULL REFERENCES document_chunks(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 1 AND 30),
    score DOUBLE PRECISION NOT NULL CHECK (score >= 0 AND score < 'Infinity'::float8),
    UNIQUE (message_id,chunk_id),
    UNIQUE (message_id,position)
);
-- The unique indexes already cover message_id.
CREATE INDEX chat_message_sources_chunk_idx ON chat_message_sources(chunk_id);

-- +goose Down
DROP TABLE chat_message_sources;
DROP TABLE chat_messages;
DROP TABLE chat_sessions;
