export interface Source {
  chunk_id: string;
  document_id: string;
  document_version_id: string;
  document_title: string;
  version_number: number;
  chunk_index: number;
  content: string;
  page_number: number | null;
  section_number: string | null;
  section_title: string | null;
  score: number;
}

export type AnswerMode = 'search' | 'no_sources' | 'generated' | 'clarification';

export interface SearchResponse {
  query: string;
  mode: 'search';
  results: Source[];
  total: number;
}

export interface ChatSession {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
}

export interface ChatSource extends Source {
  position: number;
}

export interface ChatMessageRecord {
  id: string;
  session_id: string;
  role: 'user' | 'assistant';
  content: string;
  mode: AnswerMode | null;
  client_message_id?: string;
  reply_to_id?: string;
  created_at: string;
  sources: ChatSource[];
}

export interface ChatHistory {
  session: ChatSession;
  messages: ChatMessageRecord[];
  limit: number;
  offset: number;
}

export interface ChatSessions {
  sessions: ChatSession[];
  limit: number;
  offset: number;
}

export interface ChatTurn {
  user_message_id: string;
  assistant_message_id: string;
  mode: AnswerMode;
  message: string;
  sources: ChatSource[];
  user_created_at: string;
  assistant_created_at: string;
}

// View state only: these messages are never presented as server history.
export interface LocalMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  mode: AnswerMode | null;
  sources: Source[];
}
