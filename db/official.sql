-- Texts published from official page scans or PDFs before (or beside) the UL archive.
-- Applied on its own (load-official), so the big build is never touched.
-- Rule (16 Oct 2026): the Lebanese University archive comes first. A text UL
-- already has is served from UL; this table only fills what UL lacks, and a row is
-- marked superseded the moment UL has the same kind + number + year.
CREATE TABLE IF NOT EXISTS official_text (
  id                   integer PRIMARY KEY,   -- the source's own id (SaderLex gazette id)
  source               text NOT NULL,         -- 'saderlex'
  source_name          text NOT NULL,
  source_url           text,
  kind                 text NOT NULL,
  number               text,
  number_int           integer,
  issued_on            date,
  year                 integer,
  title                text NOT NULL,
  grp                  text,                  -- ministry or sub-type as listed by the source
  pages                integer NOT NULL,
  gazette_issue        text,
  gazette_issue_date   date,
  gazette_page_from    integer,
  gazette_page_to      integer,
  pcm_url              text,
  words                integer,
  flagged_spans        integer,
  agreement            real,                  -- share of words on which the two readers agree
  status               text NOT NULL DEFAULT 'official-scan',
  body                 jsonb NOT NULL,        -- {pages:[{n, cols:[text with ‹a|b› markers]}]}
  superseded_by_law_id integer,               -- set when UL has the text
  loaded_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS official_text_kind_number ON official_text (kind, number_int, year);
CREATE INDEX IF NOT EXISTS official_text_date ON official_text (issued_on DESC);
