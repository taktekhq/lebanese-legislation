CREATE TABLE IF NOT EXISTS official_text (
  id                   integer PRIMARY KEY,   
  source               text NOT NULL,         
  source_name          text NOT NULL,
  source_url           text,
  kind                 text NOT NULL,
  number               text,
  number_int           integer,
  issued_on            date,
  year                 integer,
  title                text NOT NULL,
  grp                  text,                  
  pages                integer NOT NULL,
  gazette_issue        text,
  gazette_issue_date   date,
  gazette_page_from    integer,
  gazette_page_to      integer,
  pcm_url              text,
  words                integer,
  flagged_spans        integer,
  agreement            real,                  
  status               text NOT NULL DEFAULT 'official-scan',
  body                 jsonb NOT NULL,        
  superseded_by_law_id integer,               
  loaded_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS official_text_kind_number ON official_text (kind, number_int, year);
CREATE INDEX IF NOT EXISTS official_text_date ON official_text (issued_on DESC);