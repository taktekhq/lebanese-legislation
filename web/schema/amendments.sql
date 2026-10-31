
DROP VIEW IF EXISTS legislation_version;
DROP VIEW IF EXISTS article_in_force;
DROP TABLE IF EXISTS article_version;
DROP TABLE IF EXISTS amendment;

CREATE TABLE amendment (
  id              integer PRIMARY KEY,
  by_law_id       integer NOT NULL REFERENCES legislation (id),  
  by_article      text NOT NULL,                                
  seq             smallint NOT NULL,       
  action          text NOT NULL CHECK (action IN ('replace', 'repeal', 'insert')),
  law_id          integer REFERENCES legislation (id),  
  article_number  text,                    
  part            text,                    
  text_ar         text,                    
  old_text_ar     text,                    
  effective_on    date,                    
  status          text NOT NULL CHECK (status IN ('applied', 'partial', 'review', 'rejected')),
  reason          text,                    
  method          text NOT NULL CHECK (method IN ('pattern', 'editorial', 'model')),
  resolved_by     text,                    
  judged_by       text,                    
  judgement       text,                    
  evidence        text,                    
  UNIQUE (by_law_id, by_article, seq, article_number)
);

CREATE TABLE article_version (
  law_id          integer NOT NULL REFERENCES legislation (id),
  article_number  text NOT NULL,
  major           smallint NOT NULL,
  minor           smallint NOT NULL,
  valid_from      date,                    
  valid_to        date,                    
  state           text NOT NULL CHECK (state IN ('in_force', 'repealed')),
  text_ar         text,
  text_source     text NOT NULL CHECK (text_source IN ('centre', 'amendment', 'editorial', 'none')),
  amendment_id    integer REFERENCES amendment (id),  
  note            text,                    
  source_kind     text,                    
  source_id       text,                    
  PRIMARY KEY (law_id, article_number, major, minor)
);

CREATE VIEW article_in_force AS
WITH latest AS (
  SELECT DISTINCT ON (law_id, article_number) *
  FROM article_version ORDER BY law_id, article_number, major DESC, minor DESC
), worded AS (
  SELECT DISTINCT ON (law_id, article_number) *
  FROM article_version WHERE text_ar IS NOT NULL ORDER BY law_id, article_number, major DESC, minor DESC
)
SELECT law_id, number, position, state,
       CASE WHEN state = 'repealed' THEN NULL ELSE text_ar END AS text_ar,
       version, text_source, valid_from, amendment_id,
       (state = 'in_force' AND pending) AS pending
FROM (
  SELECT coalesce(a.law_id, v.law_id) AS law_id,
         coalesce(a.number, v.article_number) AS number,
         a.position,
         CASE WHEN a.note ~ '^\s*مادة ملغا' THEN 'repealed' ELSE coalesce(v.state, 'in_force') END AS state,
         CASE WHEN v.state = 'repealed' THEN NULL ELSE coalesce(w.text_ar, a.text_ar) END AS text_ar,
         coalesce(w.major || '.' || w.minor, '1.0') AS version,
         coalesce(w.text_source, 'centre') AS text_source,
         v.valid_from,
         v.amendment_id,
         coalesce(v.state = 'in_force' AND v.text_ar IS NULL AND (w.major IS NULL OR w.major < v.major), false) AS pending
  FROM legislation_article a
  FULL JOIN latest v ON v.law_id = a.law_id AND v.article_number = a.number
  LEFT JOIN worded w ON w.law_id = coalesce(a.law_id, v.law_id) AND w.article_number = coalesce(a.number, v.article_number)
) f;

CREATE VIEW legislation_version AS
SELECT l.id AS law_id, 1 AS version, l.issued_on AS valid_from, NULL::integer[] AS amended_by, 0 AS changes
FROM legislation l
UNION ALL
SELECT law_id, (1 + row_number() OVER (PARTITION BY law_id ORDER BY effective_on))::int,
       effective_on, array_agg(DISTINCT by_law_id ORDER BY by_law_id), count(*)::int
FROM amendment
WHERE status IN ('applied', 'partial') AND law_id IS NOT NULL AND effective_on IS NOT NULL
GROUP BY law_id, effective_on;