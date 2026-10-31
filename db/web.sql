-- What the website in web/ reads that the public dump does not carry: its text
-- functions, generated search columns, two views, four tables and their
-- indexes. Definitions match web/schema/. One transaction, safe to rerun.
-- web-schema.sh applies it after the first restore.
BEGIN;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE OR REPLACE FUNCTION ar_norm(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[ً-ٰٟـ]', '', 'g'), 'أإآٱىة', 'اااايه');

CREATE OR REPLACE FUNCTION ar_light(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[ً-ٰٟـ]', '', 'g'), 'أإآٱ', 'اااا');

CREATE OR REPLACE FUNCTION ar_digits(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(t, '٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹', '01234567890123456789');

ALTER TABLE legislation_article
  ADD COLUMN IF NOT EXISTS search_exact tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(text_ar))) STORED,
  ADD COLUMN IF NOT EXISTS search_stem  tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(text_ar))) STORED,
  ADD COLUMN IF NOT EXISTS search_fr    tsvector GENERATED ALWAYS AS (to_tsvector('french', coalesce(text_fr, ''))) STORED;

ALTER TABLE ruling
  ADD COLUMN IF NOT EXISTS search_exact tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(title || ' ' || coalesce(text_ar, '')))) STORED,
  ADD COLUMN IF NOT EXISTS search_stem  tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(title || ' ' || coalesce(text_ar, '')))) STORED;

ALTER TABLE ruling_page
  ADD COLUMN IF NOT EXISTS search_stem tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(ar_digits(text_ar)))) STORED;

-- Not in the public dump, so these stay empty: no council rulings, every page
-- indexable, no change dates, no official gazette texts.
CREATE TABLE IF NOT EXISTS council_ruling (
  law_id      integer NOT NULL,
  number      text NOT NULL,
  effect      text NOT NULL CHECK (effect IN ('annulled', 'annulled_in_part', 'not_in_force', 'suspended', 'upheld')),
  decision    text NOT NULL,
  decided_on  date NOT NULL,
  source_url  text NOT NULL,
  note_en     text NOT NULL,
  note_ar     text NOT NULL,
  PRIMARY KEY (law_id, number, decision)
);

CREATE TABLE IF NOT EXISTS noindex_page (
  kind      text NOT NULL CHECK (kind IN ('article', 'ruling', 'opinion', 'agreement', 'doctrine', 'research')),
  id        integer NOT NULL,
  position  integer NOT NULL DEFAULT 0,
  reason    text NOT NULL,
  PRIMARY KEY (kind, id, position)
);

CREATE TABLE IF NOT EXISTS page_state (
  kind          text NOT NULL CHECK (kind IN ('law', 'ruling', 'gazette', 'opinion', 'agreement', 'doctrine', 'research')),
  id            integer NOT NULL,
  content_hash  text NOT NULL,
  changed_at    timestamptz NOT NULL,
  PRIMARY KEY (kind, id)
);

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

CREATE OR REPLACE VIEW legislation_summary AS
SELECT l.*,
       (SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id) AS article_count,
       (SELECT min(e.issue_id) FROM gazette_entry e WHERE e.law_id = l.id) AS gazette_issue_id,
       (SELECT min(r.from_id) FROM reference r WHERE r.law_id = l.id
          AND r.from_type = 'legislation' AND r.relation = 'repeals') AS repealed_by
FROM legislation l;

CREATE OR REPLACE VIEW article_cited AS
SELECT a.*, l.kind, l.number AS law_number, l.issued_on, l.title AS law_title, l.status,
       'المادة ' || a.number || ' من ' || l.title AS citation
FROM legislation_article a JOIN legislation l ON l.id = a.law_id;

CREATE INDEX IF NOT EXISTS legislation_title_trgm ON legislation USING gin (ar_norm(title) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS legislation_kind_date  ON legislation (kind, issued_on DESC);
CREATE INDEX IF NOT EXISTS legislation_number     ON legislation (number_int, issued_on);
CREATE INDEX IF NOT EXISTS legislation_issued     ON legislation (issued_on DESC);
CREATE INDEX IF NOT EXISTS article_exact   ON legislation_article USING gin (search_exact);
CREATE INDEX IF NOT EXISTS article_stem    ON legislation_article USING gin (search_stem);
CREATE INDEX IF NOT EXISTS article_fr      ON legislation_article USING gin (search_fr);
CREATE INDEX IF NOT EXISTS article_number  ON legislation_article (law_id, number_int);
CREATE INDEX IF NOT EXISTS article_section ON legislation_article (section_id);
CREATE INDEX IF NOT EXISTS section_law     ON legislation_section (law_id, position);
CREATE INDEX IF NOT EXISTS ruling_exact    ON ruling USING gin (search_exact);
CREATE INDEX IF NOT EXISTS ruling_stem     ON ruling USING gin (search_stem);
CREATE INDEX IF NOT EXISTS ruling_court    ON ruling (court, decided_on DESC);
CREATE INDEX IF NOT EXISTS ruling_page_stem ON ruling_page USING gin (search_stem);
CREATE INDEX IF NOT EXISTS opinion_stem    ON opinion USING gin (to_tsvector('arabic', ar_light(coalesce(subject, '') || ' ' || coalesce(text_ar, ''))));
CREATE INDEX IF NOT EXISTS gazette_entry_law ON gazette_entry (law_id);
CREATE INDEX IF NOT EXISTS reference_to      ON reference (law_id, relation);
CREATE INDEX IF NOT EXISTS reference_article ON reference (article_centre_id);
CREATE INDEX IF NOT EXISTS graph_edge_from    ON graph_edge (from_key);
CREATE INDEX IF NOT EXISTS graph_edge_to      ON graph_edge (to_key);
CREATE INDEX IF NOT EXISTS graph_edge_to_law  ON graph_edge (to_law_id);
CREATE INDEX IF NOT EXISTS graph_edge_from_id ON graph_edge (from_type, from_id);
CREATE INDEX IF NOT EXISTS official_text_kind_number ON official_text (kind, number_int, year);
CREATE INDEX IF NOT EXISTS official_text_date ON official_text (issued_on DESC);

-- health.sh reports the database unhealthy until this row exists.
CREATE TABLE IF NOT EXISTS web_schema (
  singleton  boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  version    integer NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO web_schema (version) VALUES (1)
ON CONFLICT (singleton) DO UPDATE SET version = EXCLUDED.version, applied_at = now();

DO $$
BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'lebleg_reader') THEN
    GRANT SELECT ON council_ruling, noindex_page, page_state, official_text,
                    legislation_summary, article_cited, web_schema TO lebleg_reader;
  END IF;
END $$;

ANALYZE legislation, legislation_article, ruling, ruling_page;
COMMIT;
