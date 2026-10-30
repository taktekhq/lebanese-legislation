
DROP VIEW IF EXISTS possibly_affected;
DROP VIEW IF EXISTS graph_node;
DROP TABLE IF EXISTS graph_edge;

CREATE TABLE graph_edge (
  id            integer PRIMARY KEY,
  from_key      text NOT NULL,
  to_key        text,                    
  relation      text NOT NULL,
  source        text NOT NULL CHECK (source IN ('model', 'centre', 'parser', 'editorial')),
  status        text NOT NULL CHECK (status IN ('linked', 'no_article', 'unresolved', 'general')),
  from_type     text NOT NULL,           
  from_id       integer NOT NULL,        
  from_article  text,                    
  to_law_id     integer,                 
  to_article    text,                    
  to_cite       text,                    
  part          text,                    
  how           text,                    
  quote         text,                    
  model         text                     
);

CREATE VIEW graph_node AS
SELECT 'law/' || l.id AS key, 'legislation' AS type, l.id, NULL::text AS article,
       l.kind || coalesce(' رقم ' || l.number, '') AS label, l.title, l.issued_on AS dated, l.status
FROM legislation l
UNION ALL
SELECT 'law/' || a.law_id || '/art/' || a.number, 'article', a.law_id, a.number,
       'المادة ' || a.number, NULL, a.in_force_from, NULL
FROM legislation_article a
UNION ALL
SELECT 'ruling/' || r.id, 'ruling', r.id, NULL, coalesce(r.number::text || '/' || r.year::text, ''), r.title, r.decided_on, NULL FROM ruling r
UNION ALL
SELECT 'opinion/' || o.id, 'opinion', o.id, NULL, coalesce(o.number::text || '/' || o.year::text, ''), o.subject, o.issued_on, NULL FROM opinion o
UNION ALL
SELECT 'research/' || s.id, 'research', s.id, NULL, '', s.title, s.published_on, NULL FROM research s
UNION ALL
SELECT 'gazette/' || g.id, 'gazette', g.id, NULL, coalesce(g.number::text, ''), NULL, g.published_on, NULL FROM gazette_issue g;

CREATE VIEW possibly_affected AS
WITH general AS (
  SELECT DISTINCT from_id AS law_id, from_article AS article
  FROM graph_edge WHERE relation = 'general_repeal'
), named AS (
  SELECT DISTINCT from_id AS law_id, to_law_id, to_article
  FROM graph_edge
  WHERE from_type = 'legislation' AND status IN ('linked', 'no_article')
    AND to_law_id IS NOT NULL AND to_law_id <> from_id
    AND relation NOT IN ('note_amended_by', 'note_repealed_by', 'note_added_by')
)
SELECT g.law_id AS repealing_law_id, g.article AS repealing_article,
       n.to_law_id AS law_id, n.to_article AS article_number
FROM general g
JOIN named n ON n.law_id = g.law_id
JOIN legislation r ON r.id = g.law_id
JOIN legislation t ON t.id = n.to_law_id
WHERE (r.issued_on IS NULL OR t.issued_on IS NULL OR t.issued_on < r.issued_on)
  AND t.status = 'in_force';