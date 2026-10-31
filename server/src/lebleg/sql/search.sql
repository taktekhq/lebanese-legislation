CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS source_documents_title_trgm ON source_documents USING gin(title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS source_documents_text_trgm ON source_documents USING gin(text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS source_documents_identifier ON source_documents ((coalesce(metadata->>'identifier',metadata->>'number','')));
CREATE INDEX IF NOT EXISTS source_documents_issued_on ON source_documents ((coalesce(metadata->>'issued_on',metadata->>'date','')));
