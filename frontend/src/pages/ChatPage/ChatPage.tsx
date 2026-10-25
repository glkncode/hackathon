import { Info, Trash2 } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { ChatInput, ChatMessage, ChatWelcome, useChatController } from '../../features/chat';
import chatMessageStyles from '../../features/chat/components/ChatMessage.module.css';
import { ApiError } from '../../shared/api/client';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './ChatPage.module.css';
import { ChatDocumentPanel, DocumentDownload } from './ChatDocumentPanel';

export function ChatPage() {
  const [params] = useSearchParams();
  const sessionId = params.get('session');
  const {
    sessions,
    available,
    history,
    archive,
    ready,
    pending,
    messages,
    send,
    submit,
    draft,
    setDraft,
    attempt,
    scrollContainer,
  } = useChatController(sessionId);

  return (
    <section className={styles['chat-page']}>
      {sessionId && (
        <div className={styles['conversation-top']}>
          <h1>{history.data?.session.title ?? 'Сохранённый диалог'}</h1>
          {history.isSuccess && (
            <button
              className={cx(ui['text-button'], styles['archive-button'])}
              disabled={archive.isPending || pending}
              onClick={() => archive.mutate()}
            >
              <Trash2 size={14} />
              {archive.isPending ? 'Архивируем…' : 'В архив'}
            </button>
          )}
        </div>
      )}
      <div className={styles['chat-scroll']} ref={scrollContainer}>
        <div className={styles['chat-content']}>
          {sessionId && history.isPending && <Loading label="Загружаем диалог…" />}
          {sessionId && history.isError && (
            <ErrorNotice error={history.error} onRetry={() => void history.refetch()} />
          )}
          {archive.isError && <ErrorNotice error={archive.error} />}
          {!sessionId && sessions.isError && (
            <ErrorNotice error={sessions.error} onRetry={() => void sessions.refetch()} />
          )}
          {available === false && !sessionId && (
            <div className={styles['temporary-notice']}>
              <Info size={16} />
              <span>
                Временный диалог: сообщения хранятся только на этой странице. Серверная история пока
                недоступна.
              </span>
            </div>
          )}
          {sessionId && history.data?.session.archived_at && (
            <div className={styles['temporary-notice']}>
              <Info size={16} />
              Этот диалог в архиве. Создайте новый, чтобы задать вопрос.
            </div>
          )}
          {messages.length === 0 && !pending && (!sessionId || history.isSuccess) && (
            <ChatWelcome ready={ready} pending={pending} onSelect={setDraft} />
          )}
          {messages.length > 0 && (
            <div
              className={styles['messages']}
              role="log"
              aria-label="Сообщения диалога"
              aria-live="polite"
            >
              {messages.map((message) => (
                <div key={message.id}><ChatMessage message={message} />
                  {message.document_generated_id && <DocumentDownload id={message.document_generated_id} />}
                </div>
              ))}
            </div>
          )}
          {pending && (
            <div className={cx(styles['messages'], styles['pending-turn'])}>
              <ChatMessage
                message={{
                  id: 'pending',
                  role: 'user',
                  content: attempt.current?.text ?? '',
                  mode: null,
                  sources: [],
                }}
              />
              <Loading label="Обрабатываем сообщение…" />
            </div>
          )}
          {send.isError && <ErrorNotice error={send.error} onRetry={submit} />}
          {sessionId && history.isSuccess && !history.data?.session.archived_at &&
            <ChatDocumentPanel key={sessionId} sessionId={sessionId} updatedAt={history.dataUpdatedAt} pending={pending}
              generatedInHistory={messages.flatMap(message => message.document_generated_id ? [message.document_generated_id] : [])} />}
          {send.isError && send.error instanceof ApiError && send.error.status === 409 && (
            <p className={chatMessageStyles['search-disclaimer']}>
              Если диалог архивирован, создайте новый. При изменении источников можно повторить
              вопрос.
            </p>
          )}
        </div>
      </div>
      <div className={styles['chat-bottom']}>
        <ChatInput
          value={draft}
          onChange={(value) => {
            setDraft(value);
            if (send.isError) send.reset();
          }}
          onSend={submit}
          pending={pending}
          disabled={!ready || archive.isPending}
        />
      </div>
    </section>
  );
}
