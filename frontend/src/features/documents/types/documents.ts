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
