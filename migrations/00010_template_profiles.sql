-- +goose Up
ALTER TABLE document_templates ADD COLUMN profile JSONB;
ALTER TABLE document_templates ADD CONSTRAINT template_profile_version CHECK (
 (schema_version = 1 AND profile IS NULL) OR
 (schema_version = 2 AND jsonb_typeof(profile) = 'object' AND profile->>'profile_version' = '2' AND octet_length(profile::text) <= 196608)
);
ALTER TABLE template_generated_files ADD COLUMN generation_snapshot JSONB;
ALTER TABLE template_generated_files ADD CONSTRAINT generation_snapshot_size CHECK (
 generation_snapshot IS NULL OR (jsonb_typeof(generation_snapshot) = 'object' AND octet_length(generation_snapshot::text) <= 524288)
);

-- +goose Down
ALTER TABLE template_generated_files DROP COLUMN generation_snapshot;
ALTER TABLE document_templates DROP CONSTRAINT template_profile_version;
ALTER TABLE document_templates DROP COLUMN profile;
