-- +goose Up
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_mode_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_mode_check
    CHECK (mode IN ('search','no_sources','generated','clarification'));
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_content_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_content_check CHECK (
    (role='user' AND char_length(content) BETWEEN 1 AND 500)
    OR (role='assistant' AND char_length(content) BETWEEN 1 AND 8000 AND octet_length(content)<=32768)
);

-- Citations and security provenance are different: model output can depend on
-- ANY supplied source and earlier generated answers, even if it did not cite it.
CREATE TABLE chat_message_dependencies (
    message_id UUID NOT NULL REFERENCES chat_messages(id),
    chunk_id UUID NOT NULL REFERENCES document_chunks(id) ON DELETE RESTRICT,
    PRIMARY KEY (message_id,chunk_id)
);
CREATE INDEX chat_message_dependencies_chunk_idx ON chat_message_dependencies(chunk_id);

-- +goose Down
-- Migration 6 cannot represent generated modes or large answers. Replace them
-- with a neutral fallback before narrowing constraints; do not drop user history.
UPDATE chat_messages SET content='Содержимое ответа недоступно после отключения режима генерации.',mode='no_sources'
    WHERE mode IN ('generated','clarification') OR (role='assistant' AND char_length(content)>500);
DROP TABLE chat_message_dependencies;
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_mode_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_mode_check CHECK (mode IN ('search','no_sources'));
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_content_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_content_check CHECK (char_length(content) BETWEEN 1 AND 500);
