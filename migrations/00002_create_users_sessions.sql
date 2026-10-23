-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(64) NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(16) NOT NULL DEFAULT 'member',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_username_not_blank CHECK (username !~ '^[[:space:]]*$'),
    CONSTRAINT users_password_hash_not_empty CHECK (length(password_hash) > 0),
    CONSTRAINT users_role_valid CHECK (role IN ('admin', 'member'))
);

CREATE UNIQUE INDEX users_username_lower_uidx ON users (LOWER(username));

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Only SHA-256 hashes belong here. Raw tokens are never persisted.
    token_hash BYTEA NOT NULL,
    csrf_token_hash BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_csrf_token_hash_length CHECK (csrf_token_hash IS NULL OR octet_length(csrf_token_hash) = 32),
    CONSTRAINT sessions_expiry_valid CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX sessions_token_hash_uidx ON sessions (token_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
