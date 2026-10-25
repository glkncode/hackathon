import type {
  DocumentTemplate,
  FieldValue,
  TemplateDraft,
  TemplateField,
  TemplateProfile,
} from '../types/templates';

export function moduleTitle(template: Pick<DocumentTemplate, 'name' | 'profile'>) {
  return template.name;
}
export function documentKind(template: Pick<DocumentTemplate, 'profile'>) {
  const source = template.profile?.field_source as { name?: string } | undefined;
  const name = source?.name?.toLocaleLowerCase('ru-RU') ?? '';
  if (name.includes('распоряжения')) return 'Распоряжение';
  if (name.includes('приказа')) return 'Приказ';
  return 'Документ DOCX';
}

// Read field references for navigation only. Conditions stay on the server.
export function ruleFields(profile: TemplateProfile | undefined, ruleId: string): string[] {
  if (!Array.isArray(profile?.rules)) return [];
  const rule = profile.rules.find(
    (item: unknown) => item && typeof item === 'object' && 'id' in item && item.id === ruleId,
  );
  const keys = new Set<string>();
  const visit = (node: unknown) => {
    if (!node || typeof node !== 'object') return;
    for (const [key, value] of Object.entries(node)) {
      if ((key === 'field' || key === 'other_field') && typeof value === 'string') keys.add(value);
      else if (typeof value === 'object') visit(value);
    }
  };
  visit(rule);
  return [...keys];
}
export const formatTemplateDate = (value: string) =>
  new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  );
export const choiceLabel = (value: string) =>
  ({
    yes: 'Да',
    no: 'Нет',
    unknown: 'Требуется проверка',
    developed: 'Разработан новый документ',
    modified: 'Внесены изменения',
  })[value] ?? value;
export function progress(draft: TemplateDraft) {
  const total = draft.required_field_count;
  const completed = Math.max(0, Math.min(total, draft.completed_field_count));
  return {
    total,
    completed,
    percent: total
      ? Math.round((completed / total) * 100)
      : (draft.validation?.ready ?? draft.status === 'ready_to_generate')
        ? 100
        : 0,
  };
}
export function visibleFields(draft: TemplateDraft) {
  return draft.fields.filter(
    (field) =>
      !draft.validation?.visible_fields || draft.validation.visible_fields.includes(field.key),
  );
}
export function valueText(field: TemplateField, value: FieldValue | undefined): string {
  if (
    value === undefined ||
    value === null ||
    value === '' ||
    (Array.isArray(value) && !value.length)
  )
    return 'Не заполнено';
  if (Array.isArray(value))
    return value
      .map((item) =>
        typeof item === 'string'
          ? item
          : (field.item_fields ?? [])
              .map((f) => item[f.key])
              .filter(Boolean)
              .join(' · '),
      )
      .join('\n');
  if (field.type === 'choice') return choiceLabel(value);
  if (field.type === 'date' && /^\d{4}-\d{2}-\d{2}$/.test(value))
    return value.split('-').reverse().join('.');
  return value;
}
