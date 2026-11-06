-- ============================================================================
-- Indexes, applied after the data is loaded (building them once is far faster
-- than maintaining them row by row during a bulk load).
-- ============================================================================

-- ---------------------------------------------------------------------------
-- Search. Articles and rulings carry generated tsvector columns (exact words
-- and stems; see schema.sql); opinions use an expression index. Two Arabic
-- vectors on purpose: 'simple' matches words exactly (phrases, names),
-- 'arabic' stems them (الموجبات finds موجب).
-- ---------------------------------------------------------------------------

CREATE INDEX legislation_title_trgm ON legislation USING gin (ar_norm(title) gin_trgm_ops);
CREATE INDEX legislation_kind_date  ON legislation (kind, issued_on DESC);
CREATE INDEX legislation_number     ON legislation (number_int, issued_on);
CREATE INDEX legislation_issued     ON legislation (issued_on DESC);

CREATE INDEX article_exact  ON legislation_article USING gin (search_exact);
CREATE INDEX article_stem   ON legislation_article USING gin (search_stem);
CREATE INDEX article_fr     ON legislation_article USING gin (search_fr);
CREATE INDEX article_number ON legislation_article (law_id, number_int);
CREATE INDEX article_section ON legislation_article (section_id);
CREATE INDEX section_law    ON legislation_section (law_id, position);

CREATE INDEX ruling_exact   ON ruling USING gin (search_exact);
CREATE INDEX ruling_stem    ON ruling USING gin (search_stem);
CREATE INDEX ruling_court   ON ruling (court, decided_on DESC);
-- a year's list of rulings (/rulings/year/N/): its count and its page, in order, without reading the table
CREATE INDEX ruling_year    ON ruling (coalesce(year, 0), decided_on DESC NULLS LAST, id DESC);

CREATE INDEX opinion_stem   ON opinion USING gin (to_tsvector('arabic', ar_light(coalesce(subject, '') || ' ' || coalesce(text_ar, ''))));
CREATE INDEX gazette_entry_law ON gazette_entry (law_id);
CREATE INDEX reference_to      ON reference (law_id, relation);
CREATE INDEX reference_article ON reference (article_centre_id);

-- The graph (db/graph.sql).
CREATE INDEX IF NOT EXISTS graph_edge_from ON graph_edge (from_key);
CREATE INDEX IF NOT EXISTS graph_edge_to ON graph_edge (to_key);
CREATE INDEX IF NOT EXISTS graph_edge_to_law ON graph_edge (to_law_id);
CREATE INDEX IF NOT EXISTS graph_edge_from_id ON graph_edge (from_type, from_id);

-- The full judgments' machine readings: a ruling found by words only its
-- judgment holds (docs/DATABASE.md, "Search").
CREATE INDEX ruling_page_stem ON ruling_page USING gin (search_stem);

-- ---------------------------------------------------------------------------
-- Lists. Every list on the site, the API and the MCP server pages with a
-- cursor (internal/cursor): the next page is a WHERE on the sort key, which
-- one of these answers in about a millisecond at any depth. There is no
-- OFFSET anywhere: page 2,000 of /laws/ by OFFSET read 80,000 laws, 835 ms on
-- the XPS and 3 to 9 s on the container under a crawl (6 Nov 2026).
--
-- A list sorted newest first with the undated last sorts on
-- coalesce(date, '0001-01-01'), then id, both descending: one key that an
-- index scanned backwards answers, and a cursor compares as a row.
-- ---------------------------------------------------------------------------

-- /laws/, its filters and the year lists: by issue date
CREATE INDEX legislation_list_date   ON legislation ((coalesce(issued_on, date '0001-01-01')), id);
CREATE INDEX legislation_list_kind   ON legislation (kind, (coalesce(issued_on, date '0001-01-01')), id);
CREATE INDEX legislation_list_status ON legislation (status, (coalesce(issued_on, date '0001-01-01')), id);
-- /api/v1/laws/ and the MCP list_legislation: by publication date (the
-- Gazette's, else the issue date); both leave out the undated
CREATE INDEX legislation_pub_date ON legislation ((coalesce(gazette_date, issued_on)), id);
CREATE INDEX legislation_pub_kind ON legislation (kind, (coalesce(gazette_date, issued_on)), id);

CREATE INDEX ruling_list_date  ON ruling ((coalesce(decided_on, date '0001-01-01')), id);
CREATE INDEX ruling_list_court ON ruling (court, (coalesce(decided_on, date '0001-01-01')), id);
CREATE INDEX ruling_list_year  ON ruling ((coalesce(year, 0)), (coalesce(decided_on, date '0001-01-01')), id);

CREATE INDEX gazette_list_date ON gazette_issue ((coalesce(published_on, date '0001-01-01')), id);
CREATE INDEX gazette_pub_date  ON gazette_issue (published_on, id);

CREATE INDEX opinion_list_date   ON opinion ((coalesce(issued_on, date '0001-01-01')), id);
CREATE INDEX agreement_list_date ON agreement ((coalesce(signed_on, date '0001-01-01')), id);
CREATE INDEX research_list_date  ON research ((coalesce(published_on, date '0001-01-01')), id);
-- doctrine_article lists by id (its primary key); a law's articles by
-- position (legislation_article's primary key, law_id and position)

-- ---------------------------------------------------------------------------
-- Search by words, as search engines score it (internal/mcpserver/search_pg.go):
-- one index lookup per word of the question, each word weighted by how rare
-- it is, so a text that holds every word comes first and one missing only a
-- common word comes next, all in one query; the full text is read for the top
-- 50 only. search_term says how many texts hold each word in each index (its
-- rarity), so nothing is counted at search time; term '' holds the index's
-- row count. It is a materialized view, made again by every build: the data
-- export (internal/export) copies ordinary tables only, and this is derived.
-- ---------------------------------------------------------------------------

CREATE INDEX legislation_title_exact  ON legislation USING gin (to_tsvector('simple', ar_norm(title)));
CREATE INDEX legislation_title_stem   ON legislation USING gin (to_tsvector('arabic', ar_light(title)));
CREATE INDEX legislation_title_en_fts ON legislation_title_en USING gin (to_tsvector('english', title_en));

-- tsvector_to_array has each word of a text once, so count(*) is the number
-- of texts holding it. (ts_stat would count the same, but it reads its query
-- as text when the view is refreshed, and Postgres 17+ refreshes with a
-- search_path that finds no table.)
CREATE MATERIALIZED VIEW search_term AS
            SELECT 'article_exact'::text AS corpus, t AS term, count(*)::int AS docs FROM legislation_article, unnest(tsvector_to_array(search_exact)) t GROUP BY t
  UNION ALL SELECT 'article_stem', t, count(*)::int FROM legislation_article, unnest(tsvector_to_array(search_stem)) t GROUP BY t
  UNION ALL SELECT 'article_fr', t, count(*)::int FROM legislation_article, unnest(tsvector_to_array(search_fr)) t GROUP BY t
  UNION ALL SELECT 'ruling_exact', t, count(*)::int FROM ruling, unnest(tsvector_to_array(search_exact)) t GROUP BY t
  UNION ALL SELECT 'ruling_stem', t, count(*)::int FROM ruling, unnest(tsvector_to_array(search_stem)) t GROUP BY t
  UNION ALL SELECT 'law_title', t, count(*)::int FROM legislation, unnest(tsvector_to_array(to_tsvector('simple', ar_norm(title)))) t GROUP BY t
  UNION ALL SELECT 'law_title_stem', t, count(*)::int FROM legislation, unnest(tsvector_to_array(to_tsvector('arabic', ar_light(title)))) t GROUP BY t
  UNION ALL SELECT 'law_title_en', t, count(*)::int FROM legislation_title_en, unnest(tsvector_to_array(to_tsvector('english', title_en))) t GROUP BY t
  -- each index's row count, as term ''
  UNION ALL SELECT c, '', n FROM (SELECT count(*)::int AS n FROM legislation_article) a, unnest(ARRAY['article_exact', 'article_stem']) c
  UNION ALL SELECT 'article_fr', '', count(*)::int FROM legislation_article WHERE text_fr ~ '\S'
  UNION ALL SELECT c, '', n FROM (SELECT count(*)::int AS n FROM ruling) r, unnest(ARRAY['ruling_exact', 'ruling_stem']) c
  UNION ALL SELECT c, '', n FROM (SELECT count(*)::int AS n FROM legislation) l, unnest(ARRAY['law_title', 'law_title_stem']) c
  UNION ALL SELECT 'law_title_en', '', count(*)::int FROM legislation_title_en
  -- each index's average text length, as term '#len': BM25 scores a match in
  -- a short text above one in a long text
  UNION ALL SELECT c, '#len', n FROM (SELECT avg(text_len)::int AS n FROM legislation_article) a, unnest(ARRAY['article_exact', 'article_stem']) c
  UNION ALL SELECT 'article_fr', '#len', avg(length(text_fr))::int FROM legislation_article WHERE text_fr ~ '\S'
  UNION ALL SELECT c, '#len', n FROM (SELECT avg(text_len)::int AS n FROM ruling) r, unnest(ARRAY['ruling_exact', 'ruling_stem']) c
  UNION ALL SELECT c, '#len', n FROM (SELECT avg(length(title))::int AS n FROM legislation) l, unnest(ARRAY['law_title', 'law_title_stem']) c
  UNION ALL SELECT 'law_title_en', '#len', avg(length(title_en))::int FROM legislation_title_en;
CREATE UNIQUE INDEX search_term_key ON search_term (corpus, term);
