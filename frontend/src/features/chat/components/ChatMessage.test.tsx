import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, expectTypeOf, it } from 'vitest';
import type { AnswerMode, ChatMessageRecord, ChatTurn, LocalMessage, Source } from '../types/chat';
import { ChatMessage } from './ChatMessage';
import { SourceCard } from './SourceCard';
import sourceStyles from './SourceCard.module.css';

const source: Source = {
  chunk_id: 'chunk',
  document_id: 'document',
  document_version_id: 'version',
  document_title: 'Инструкция',
  version_number: 2,
  chunk_index: 0,
  content: '<script>alert("test")</script>',
  page_number: null,
  section_number: null,
  section_title: null,
  score: 0.4,
};

function renderSource(value: Source) {
  const client = new QueryClient();
  const result = renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <SourceCard source={value} index={0} />
    </QueryClientProvider>,
  );
  client.clear();
  return result;
}

function renderMessage(mode: AnswerMode, content: string, sources: Source[] = []) {
  const client = new QueryClient();
  const result = renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <ChatMessage message={{ id: 'answer', role: 'assistant', content, mode, sources }} />
    </QueryClientProvider>,
  );
  client.clear();
  return result;
}

describe('answer modes', () => {
  it('supports the server modes throughout response and view types', () => {
    expectTypeOf<AnswerMode>().toEqualTypeOf<
      'search' | 'no_sources' | 'generated' | 'clarification' | 'document'
    >();
    expectTypeOf<ChatTurn['mode']>().toEqualTypeOf<AnswerMode>();
    expectTypeOf<ChatMessageRecord['mode']>().toEqualTypeOf<AnswerMode | null>();
    expectTypeOf<LocalMessage['mode']>().toEqualTypeOf<AnswerMode | null>();
  });

  it('labels a generated answer as AI and displays the server text without the search disclaimer', () => {
    const content = 'Служебную записку необходимо согласовать с руководителем.';
    const html = renderMessage('generated', content);
    expect(html).toContain('Ответ помощника');
    expect(html).toContain('aria-label="Ответ помощника"');
    expect(html).toContain(content);
    expect(html).not.toContain('Поиск по документам');
    expect(html).not.toContain('Поиск не формирует генеративный ответ');
    expect(html).not.toContain('Сохранённые источники сейчас недоступны');
  });

  it('renders the source cards returned with a generated answer', () => {
    const html = renderMessage('generated', 'Ответ с цитатой.', [source]);
    expect(html).toContain('Источники');
    expect(html).toContain('class="' + sourceStyles['source-card'] + '"');
    expect(html).toContain(source.document_title);
    expect(html).toContain('Версия 2');
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('Поиск не формирует генеративный ответ');
  });

  it('renders a clarification with no sources as a question without inferring revoked ACL', () => {
    const question = 'Уточните, пожалуйста: какой документ нужно согласовать?';
    const html = renderMessage('clarification', question);
    expect(html).toContain('Уточняющий вопрос');
    expect(html).toContain('aria-label="Уточняющий вопрос"');
    expect(html).toContain(question);
    expect(html).not.toContain('Сохранённые источники сейчас недоступны');
    expect(html).not.toContain('изменились права доступа');
    expect(html).not.toContain('Поиск не формирует генеративный ответ');
    expect(html).not.toContain('class="' + sourceStyles['source-card'] + '"');
  });

  it('labels search and explains that the source excerpts are not a generated answer', () => {
    const html = renderMessage('search', 'Найдены подходящие положения инструкции.', [source]);
    expect(html).toContain('Поиск по документам');
    expect(html).toContain('Поиск не формирует генеративный ответ');
    expect(html).toContain('class="' + sourceStyles['source-card'] + '"');
    expect(html).not.toContain('Ответ помощника');
  });

  it('does not infer ACL changes for a search with no returned sources', () => {
    const html = renderMessage('search', 'Серверный текст поиска.');
    expect(html).toContain('Поиск по документам');
    expect(html).not.toContain('Сохранённые источники сейчас недоступны');
    expect(html).not.toContain('изменились права доступа');
  });

  it('displays the no_sources server message without creating source cards', () => {
    const content = 'В доступных документах не найдена информация для ответа.';
    const html = renderMessage('no_sources', content);
    expect(html).toContain('Информация не найдена');
    expect(html).toContain(content);
    expect(html).not.toContain('class="' + sourceStyles['source-card'] + '"');
    expect(html).not.toContain('Поиск не формирует генеративный ответ');
  });

  it.each(['generated', 'clarification'] as const)(
    'shows the server replacement verbatim for a redacted %s answer',
    (mode) => {
      const replacement = 'Содержимое ответа недоступно из-за изменения прав доступа к документам.';
      const html = renderMessage(mode, replacement);
      expect(html).toContain(replacement);
      expect(html).not.toContain('class="' + sourceStyles['source-card'] + '"');
      expect(html).not.toContain('Сохранённые источники сейчас недоступны');
      expect(html).not.toContain('Поиск не формирует генеративный ответ');
    },
  );
});

describe('safe citation rendering', () => {
  it('renders Markdown lists, emphasis and tables for long answers', () => {
    const html = renderMessage(
      'generated',
      '## Порядок\n\n**Подготовка**\n\n1. Проверить\n2. Согласовать\n\n| Документ | Срок |\n| --- | --- |\n| Записка | 2 дня |',
    );
    expect(html).toContain('<h2>Порядок</h2>');
    expect(html).toContain('<strong>Подготовка</strong>');
    expect(html).toContain('<ol>');
    expect(html).toContain('<table>');
    expect(html).toContain('aria-label="Таблица в ответе"');
  });

  it('does not execute answer HTML, dangerous links or remote images', () => {
    const html = renderMessage(
      'generated',
      '<script>alert(1)</script>\n\n[Ссылка](javascript:alert)\n\n![Схема](https://example.org/tracker.png)',
    );
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).not.toContain('<img');
    expect(html).toContain('Схема');
  });

  it('escapes document text instead of rendering HTML', () => {
    const html = renderSource(source);
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>');
  });

  it('does not invent missing source metadata or turn rank into confidence', () => {
    const html = renderSource(source);
    expect(html).toContain('Версия 2');
    expect(html).not.toContain('Страница');
    expect(html).not.toContain('Пункт');
    expect(html).not.toContain('40%');
  });

  it('uses exact API metadata and allows expanding a long excerpt', () => {
    const html = renderSource({
      ...source,
      page_number: 13,
      section_number: '4.6',
      content: 'Документ '.repeat(100),
    });
    expect(html).toContain('Страница 13');
    expect(html).toContain('Пункт 4.6');
    expect(html).toContain('Показать фрагмент целиком');
    expect(html).toContain('aria-expanded="false"');
  });

  it('labels an empty answer and renders user messages as text', () => {
    const html = renderToStaticMarkup(
      <ChatMessage
        message={{
          id: 'empty',
          role: 'assistant',
          content: 'Нет результатов',
          mode: 'no_sources',
          sources: [],
        }}
      />,
    );
    expect(html).toContain('Информация не найдена');
    const userHtml = renderToStaticMarkup(
      <ChatMessage
        message={{
          id: 'user',
          role: 'user',
          content: '<img onerror="test">',
          mode: null,
          sources: [],
        }}
      />,
    );
    expect(userHtml).toContain('&lt;img');
    expect(userHtml).not.toContain('<img');
  });
});
