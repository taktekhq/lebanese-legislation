-- The graph: every link we know between two items of the corpus, in one
-- table, so the whole graph comes out of the database with one query.
-- Created after amendments.sql, and again on its own by
-- "build-db -only graph".
--
-- An item is addressed by a key that says what it is:
--   law/<id>                 a text (law, decree, decision...)
--   law/<id>/art/<number>    one of its articles
--   ruling/<id>, opinion/<id>, research/<id>, gazette/<id>
-- graph_node lists every item; graph_edge every link. An article belongs to
-- its text by its key, so there are no "contains" edges.

DROP VIEW IF EXISTS possibly_affected;
DROP VIEW IF EXISTS graph_node;
DROP TABLE IF EXISTS graph_edge;

-- One link from one item to another.
--   relation  what the source item does to or with the target:
--             changes:  replace, amend, repeal, insert, suspend, extend,
--                       general_repeal (no named target)
--             links:    cites, applies, derogates, interprets (a ruling explains
--                       a provision), publishes (a gazette
--                       issue publishes a text)
--             notes:    note_amended_by, note_repealed_by, note_added_by
--                       (the Centre's own note inside an article, pointing at
--                       the text that changed it)
--   source    model    read by a model from an article (db/model/citations.jsonl)
--                      or a ruling (db/model/ruling_citations.jsonl);
--                      the model wrote the citation, the build matched it
--             centre   given as a link on the Centre's pages
--             parser   read by the build's patterns (the amendment table)
--             editorial  a text that changes every sum of money in another,
--                      listed by a person (db/amounts.tsv), linked to each
--                      article in scope that states a sum
--   status    linked      the target is in our data
--             no_article  the text is, but not an article with that number
--             unresolved  the target isn't in our data (yet), or its
--                         citation fits several texts: to_cite says what was cited
--             general     names no target (general_repeal)
CREATE TABLE graph_edge (
  id            integer PRIMARY KEY,
  from_key      text NOT NULL,
  to_key        text,                    -- NULL unless linked
  relation      text NOT NULL,
  source        text NOT NULL CHECK (source IN ('model', 'centre', 'parser', 'editorial')),
  status        text NOT NULL CHECK (status IN ('linked', 'no_article', 'unresolved', 'general')),
  from_type     text NOT NULL,           -- legislation, ruling, opinion, research, gazette
  from_id       integer NOT NULL,        -- the source item's id (the text's, for an article)
  from_article  text,                    -- the source article's number, when the source is an article
  to_law_id     integer,                 -- the target text, when matched
  to_article    text,                    -- the target article, as cited; NULL for the text as a whole
  to_cite       text,                    -- the target as cited, in a stable form ("lb/law/2001-08-02/328/art/73")
  part          text,                    -- the part of the target named ("الفقرة الثانية")
  how           text,                    -- how the target was matched, or why not
  quote         text,                    -- the source's own words naming the target
  model         text                     -- the model that read it, for source 'model'
);

-- Every item, with a label and a date, whether or not anything links to it.
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

-- Texts and articles possibly affected by a general repeal ("all provisions
-- contrary to this law are repealed"): the earlier texts the repealing text
-- itself links to, and the articles it names in them. A list to check, never
-- a finding that anything was repealed.
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
