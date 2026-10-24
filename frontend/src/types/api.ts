export interface User {
  id: string;
  username: string;
  role: 'admin' | 'member';
}

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

export interface DocumentRecord {
  id: string;
  title: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  active_version_id: string | null;
}

export interface DocumentVersion {
  id: string;
  document_id: string;
  version_number: number;
  original_filename: string;
  mime_type: string;
  size_bytes: number;
  created_at: string;
  updated_at: string;
  status: 'uploaded' | 'processing' | 'ready' | 'failed';
  sha256?: string;
  ingestion_job_id: string | null;
}

export interface DocumentDetails extends DocumentRecord {
  latest_version: DocumentVersion;
  active_version: DocumentVersion;
}

export interface Collection<T> {
  items: T[];
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
