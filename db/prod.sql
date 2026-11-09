-- db/prod.sql: what turns the -db image into the -db-prod image.
--
-- ghcr.io/taktekhq/lebanese-legislation-db is the public dataset image: the
-- database exactly as the published data defines it. It does not change here.
--
-- ghcr.io/taktekhq/lebanese-legislation-db-prod is -db plus read-optimized
-- copies for the servers of the all-in-one image (today the MCP server). It adds no
-- data: every table below is computed from -db by this file, and can be
-- rebuilt from it at any time. The all-in-one image (-all, what
-- lebaneselegislation.com runs) is built from -db-prod.
--
--   possibly_affected_t  the rows of the possibly_affected view stored as a
--                      table, with indexes. The view itself is untouched, so
--                      db/graph.sql and data/schema.sql still apply.
--   law_card           one row per law: the law summary every MCP reply
--                      carries (internal/mcpserver lawCols).
--   article_card       one row per article, with its wording in force
--                      resolved (internal/mcpserver articleCols).
--
-- The MCP server reads these tables only when law_card and article_card
-- carry the version mark it expects (internal/mcpserver prodVersion), and
-- computes the same columns otherwise, so it still runs on -db and never
-- fails on an image from an older prod.sql. The file is idempotent: running
-- it again rebuilds the tables. The Parquet export and -db never see it.
-- Measured 9 Nov 2026: get_article p95 for 6 calls at once went from 1,267 ms
-- to about 90 ms on one CPU, with byte-identical replies.

SET default_transaction_read_only = off;

-- possibly_affected is a view: a self-join of graph_edge on every read
-- (214 ms of a 232 ms get_article). As a table it is an index lookup.
DROP TABLE IF EXISTS possibly_affected_t;
CREATE TABLE possibly_affected_t AS SELECT * FROM possibly_affected;
CREATE INDEX possibly_affected_t_target ON possibly_affected_t (law_id, article_number);
CREATE INDEX possibly_affected_t_repealing ON possibly_affected_t (repealing_law_id);

CREATE INDEX IF NOT EXISTS prod_article_law_number ON legislation_article (law_id, number);
CREATE INDEX IF NOT EXISTS prod_graph_edge_to_article ON graph_edge (to_law_id, to_article);

-- law_card: the columns of internal/mcpserver lawCols, in that order. The
-- inner join to legislation_kind is the server's: a text whose kind has no
-- row there is not served.
DROP TABLE IF EXISTS law_card;
CREATE TABLE law_card AS
SELECT l.id, l.kind, k.name_en AS kind_en, l.number, to_char(l.issued_on, 'YYYY-MM-DD') AS issued_on, l.title, l.status,
  l.gazette_number, to_char(l.gazette_date, 'YYYY-MM-DD') AS gazette_date,
  (SELECT min(ge.issue_id) FROM gazette_entry ge WHERE ge.law_id = l.id) AS gazette_issue_id,
  (SELECT count(*) FROM legislation_article la WHERE la.law_id = l.id)::int AS article_count,
  c.slug AS code_slug, c.name_en AS code_name_en, c.name_fr AS code_name_fr, sp.url AS source_url,
  nullif(te.title_en, '') AS title_en, ps.changed_at
FROM legislation l
  JOIN legislation_kind k ON k.name_ar = l.kind
  LEFT JOIN code c ON c.law_id = l.id
  LEFT JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
  LEFT JOIN legislation_title_en te ON te.law_id = l.id
  LEFT JOIN page_state ps ON ps.kind = 'law' AND ps.id = l.id;
ALTER TABLE law_card ADD PRIMARY KEY (id);

-- article_card: the columns of internal/mcpserver articleCols, in that
-- order: the article, its latest and latest worded versions resolved.
DROP TABLE IF EXISTS article_card;
CREATE TABLE article_card AS
SELECT a.law_id, a.position, a.number, a.note, a.text_ar, a.text_fr, a.in_force_from, a.section_id,
  CASE WHEN st.state = 'repealed' OR v.state = 'repealed' THEN NULL ELSE coalesce(w.text_ar, a.text_ar) END AS current_text,
  coalesce(w.major || '.' || w.minor, '1.0') AS version, st.state,
  st.state = 'in_force' AND coalesce(v.state = 'in_force' AND v.text_ar IS NULL AND (w.major IS NULL OR w.major < v.major), false) AS pending,
  v.valid_from,
  (al.status <> 'in_force'
   OR EXISTS (SELECT 1 FROM amendment x WHERE x.law_id = a.law_id
     AND x.article_number IS NULL AND x.action = 'repeal' AND x.part IS NULL AND x.status = 'applied')
   OR EXISTS (SELECT 1 FROM council_ruling cr WHERE cr.law_id = a.law_id AND (cr.number = '' OR cr.number = a.number) AND cr.effect IN ('annulled', 'not_in_force'))) AS annulled
FROM legislation_article a
  JOIN legislation al ON al.id = a.law_id
  LEFT JOIN LATERAL (SELECT av.state, av.text_ar, av.major, av.valid_from FROM article_version av
    WHERE av.law_id = a.law_id AND av.article_number = a.number ORDER BY av.major DESC, av.minor DESC LIMIT 1) v ON true
  LEFT JOIN LATERAL (SELECT aw.text_ar, aw.major, aw.minor FROM article_version aw
    WHERE aw.law_id = a.law_id AND aw.article_number = a.number AND aw.text_ar IS NOT NULL ORDER BY aw.major DESC, aw.minor DESC LIMIT 1) w ON true
  CROSS JOIN LATERAL (SELECT CASE WHEN a.note ~ '^\s*مادة ملغا' THEN 'repealed' ELSE coalesce(v.state, 'in_force') END AS state) st;
ALTER TABLE article_card ADD PRIMARY KEY (law_id, position);
CREATE INDEX article_card_number ON article_card (law_id, number);

-- The server reads these tables only when both marks match its own
-- (internal/mcpserver prodVersion): change both when a column changes.
COMMENT ON TABLE law_card IS 'lebleg-prod 1';
COMMENT ON TABLE article_card IS 'lebleg-prod 1';

-- The server roles exist in the image (db/bake.sh); a test database may
-- have neither.
DO $$
DECLARE r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['lebleg_web', 'lebleg_reader'] LOOP
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('GRANT SELECT ON possibly_affected_t, law_card, article_card TO %I', r);
    END IF;
  END LOOP;
END $$;

ANALYZE possibly_affected_t;
ANALYZE legislation_article;
ANALYZE graph_edge;
ANALYZE law_card;
ANALYZE article_card;
