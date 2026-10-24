export type FieldValue = string | string[] | Record<string, string>[] | null;
export interface RuleSource {
  name: string;
  version?: string;
  section?: string;
}
export interface TemplateField {
  key: string;
  label: string;
  question: string;
  description: string;
  type:
    | 'text'
    | 'multiline_text'
    | 'date'
    | 'person'
    | 'person_list'
    | 'text_list'
    | 'choice'
    | 'repeater';
  required: boolean;
  max_length: number;
  options?: string[];
  item_fields?: { key: string; label: string; required: boolean; max_length?: number }[];
  minimum_items?: number;
  maximum_items?: number;
  source?: RuleSource;
  visible_if?: unknown;
  required_if?: unknown;
}
export interface TemplateProfile {
  profile_version: number;
  profile_id: string;
  revision: string;
  rules_version: string;
  engine_version: string;
  template_kind: string;
  fields: TemplateField[];
  [key: string]: unknown;
}
export interface DocumentTemplate {
  id: string;
  document_id: string;
  document_version_id: string;
  name: string;
  description: string;
  enabled: boolean;
  fields: TemplateField[];
  schema_version: number;
  profile?: TemplateProfile;
  support_status?: 'needs_configuration';
  created_at: string;
  updated_at: string;
}
export interface ApprovalRoute {
  document_kind: string;
  status: 'recommendation';
  note: string;
  required_departments: string[];
  additional_departments: string[];
  conditional_departments: {
    name: string;
    condition: string;
    applicable: boolean;
    row_id: string;
    source: RuleSource;
  }[];
  rows: {
    row_id: string;
    description: string;
    applicable: boolean;
    applicability_condition: unknown;
    required_departments: string[];
    source: RuleSource;
  }[];
}
export interface TemplateValidation {
  ready: boolean;
  missing_fields: TemplateField[];
  invalid_fields: { key: string; code: string }[];
  next_question: TemplateField | null;
  visible_fields?: string[];
  required_fields?: string[];
  checks?: {
    rule_id: string;
    category: 'blocking' | 'warning' | 'manual_review' | 'info';
    source: RuleSource;
    description: string;
    result: string;
    method: string;
  }[];
  approval_route?: ApprovalRoute;
  profile_version?: number;
  profile_id?: string;
  profile_revision?: string;
  rules_version?: string;
  engine_version?: string;
}
export interface TemplateDraft {
  id: string;
  template_id: string;
  document_version_id: string;
  owner_id: string;
  values: Record<string, FieldValue>;
  status: string;
  revision: number;
  fields: TemplateField[];
  schema_version: number;
  profile?: TemplateProfile;
  validation?: TemplateValidation;
  required_field_count: number;
  completed_field_count: number;
  created_at: string;
  updated_at: string;
}
export interface GenerationSnapshot {
  profile: TemplateProfile;
  values: Record<string, FieldValue>;
  validation: TemplateValidation;
  source_document_version_id: string;
  draft_revision: number;
  engine_version: string;
  project_status: 'draft_project';
}
export interface GeneratedDocument {
  id: string;
  draft_id: string;
  template_id: string;
  document_version_id: string;
  draft_revision: number;
  filename: string;
  mime_type: string;
  size_bytes: number;
  sha256: string;
  created_at: string;
  generation_snapshot?: GenerationSnapshot;
}
export interface GenerationResult {
  generated_document_id: string;
  draft_id: string;
  draft_revision: number;
  filename: string;
  status: 'ready';
  download_url: string;
  generation_snapshot?: GenerationSnapshot;
}
export interface TemplatePage<T> {
  items: T[] | null;
  limit: number;
  offset: number;
}
