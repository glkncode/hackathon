export { templatesApi } from './api/templates';
export { DraftCard } from './components/DraftCard';
export { TemplateCard } from './components/TemplateCard';
export { TemplateDraftEditor } from './components/TemplateDraft';
export { TemplateFieldRenderer } from './components/TemplateFieldRenderer';
export { ApprovalRoute, ValidationReport } from './components/ValidationReport';
export { useTemplateCatalogController } from './hooks/useTemplateCatalogController';
export { useTemplateResult } from './hooks/useTemplateResult';
export { loadTemplateDraft } from './hooks/useTemplates';
export { formatTemplateDate, moduleTitle, ruleFields, valueText } from './lib/templateDisplay';
export type {
  DocumentTemplate,
  FieldValue,
  TemplateDraft,
  TemplateField,
  TemplateProfile,
  TemplateValidation,
} from './types/templates';
