-- +goose Up
CREATE TABLE chat_document_workflows (
    session_id UUID PRIMARY KEY REFERENCES chat_sessions(id),
    owner_id UUID NOT NULL REFERENCES users(id),
    draft_id UUID REFERENCES template_drafts(id),
    stage TEXT NOT NULL CHECK (stage IN ('selecting_template','collecting_fields','reviewing','completed','paused','cancelled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    reviewed_revision BIGINT NOT NULL DEFAULT 0,
    expected_field TEXT NOT NULL DEFAULT '',
    candidate_ids JSONB NOT NULL DEFAULT '[]',
    generated_id UUID REFERENCES template_generated_files(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE chat_messages ADD COLUMN document_draft_id UUID REFERENCES template_drafts(id);
ALTER TABLE chat_messages ADD COLUMN document_generated_id UUID REFERENCES template_generated_files(id);
ALTER TABLE chat_messages ADD COLUMN document_action_hash TEXT;
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_mode_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_mode_check CHECK (mode IN ('search','no_sources','generated','clarification','document'));

-- +goose Down
-- Keep old conversations and all drafts. New document text cannot retain its
-- dynamic ACL projection after these links are removed, so redact that text.
UPDATE chat_messages SET content='Содержимое ответа недоступно из-за изменения прав доступа к документам.'
WHERE document_draft_id IS NOT NULL OR document_generated_id IS NOT NULL;
UPDATE chat_messages SET mode='no_sources' WHERE mode='document';
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_mode_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_mode_check CHECK (mode IN ('search','no_sources','generated','clarification'));
ALTER TABLE chat_messages DROP COLUMN document_draft_id;
ALTER TABLE chat_messages DROP COLUMN document_generated_id;
ALTER TABLE chat_messages DROP COLUMN document_action_hash;
DROP TABLE chat_document_workflows;
