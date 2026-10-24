-- +goose Up
-- CHECK accepts SQL NULL unless explicitly rejected. Preserve v1 NULL profiles,
-- but require the complete v2 envelope even for repository-level inserts.
ALTER TABLE document_templates DROP CONSTRAINT template_profile_version;
ALTER TABLE document_templates ADD CONSTRAINT template_profile_version CHECK (COALESCE(
 (schema_version = 1 AND profile IS NULL) OR
 (schema_version = 2 AND jsonb_typeof(profile) = 'object' AND profile->>'profile_version' = '2' AND octet_length(profile::text) <= 196608), FALSE)
);

-- +goose Down
ALTER TABLE document_templates DROP CONSTRAINT template_profile_version;
ALTER TABLE document_templates ADD CONSTRAINT template_profile_version CHECK (
 (schema_version = 1 AND profile IS NULL) OR
 (schema_version = 2 AND jsonb_typeof(profile) = 'object' AND profile->>'profile_version' = '2' AND octet_length(profile::text) <= 196608)
);
