-- +goose Up
-- Private dialogue boundary, recorded only after a validated resolver decision.
-- Original question text stays unchanged; no search/model text is persisted.
ALTER TABLE chat_messages ADD COLUMN starts_new_topic BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chat_messages ADD CONSTRAINT chat_topic_user_only CHECK (role='user' OR NOT starts_new_topic);

-- +goose Down
ALTER TABLE chat_messages DROP CONSTRAINT chat_topic_user_only;
ALTER TABLE chat_messages DROP COLUMN starts_new_topic;
