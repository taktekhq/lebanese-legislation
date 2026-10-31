CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE semantic_chunks (
 model text NOT NULL,kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
 title text NOT NULL,text text NOT NULL,hash text NOT NULL,chunk_start integer NOT NULL,chunk_end integer NOT NULL,
 embedding halfvec(768) NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(model,kind,source_id,part,lang,hash,chunk_start),
 CHECK(chunk_start>=0 AND chunk_end>chunk_start AND chunk_end-chunk_start<=900));
CREATE TABLE semantic_documents (
 model text NOT NULL,kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
 hash text NOT NULL,seen_run text NOT NULL,PRIMARY KEY(model,kind,source_id,part,lang));
CREATE INDEX semantic_chunks_source ON semantic_chunks(model,kind,source_id,part,lang);
