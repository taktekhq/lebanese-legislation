-- What we read ourselves from the Official Gazette's page images, for texts
-- whose typed copy lacks it, from gazette_annex.jsonl (one JSON object per
-- line), made by lebleg_annex.py in taktekhq/lebgazette-data:
--   table_row: a row of a table annexed to a text (Law 243/2012's violation
--     tables), category, row number, articles, violation, points, notes, and
--     the fine range Article 374 gives its category (2012 amounts);
--   article_original: an article's wording as first published (Articles 49
--     to 54 of the Social Security Law, 1963), shown beside the Lebanese
--     University's text, never instead of it.
-- Machine transcription by two Gemini readings compared word by word; where
-- they differed, a Gemini model decided from the page (decided_by, with every
-- candidate in readings). Unofficial. Nizar, 5 Nov 2026: "No one will [check
-- them], just put them live. And the students will check whatever they are
-- studying." anchor is the row's address on the page and in topic cites:
-- annex-<category>-<serial> or original-<number>.
CREATE TABLE gazette_annex (
  law_id              integer NOT NULL REFERENCES legislation (id),
  anchor              text NOT NULL,
  kind                text NOT NULL CHECK (kind IN ('table_row', 'article_original')),
  block               text,
  category            text,
  table_title         text,
  serial              integer,
  article             text,
  extra_articles      text,
  violation           text,
  points              text,
  notes               text,
  fine_min            bigint,
  fine_max            bigint,
  number              text,
  text_ar             text,
  gazette_issue       text NOT NULL,
  gazette_date        date NOT NULL,
  gazette_pages       text NOT NULL,
  source_url          text NOT NULL,
  replaced_by_law_id  integer,
  replaced_by_article text,
  decided_by          text,
  readings            jsonb,
  read_on             date NOT NULL,
  PRIMARY KEY (law_id, anchor)
);
CREATE TEMP TABLE gazette_annex_line (line text);
-- csv with quote and delimiter bytes that never occur: one line, one value,
-- backslashes kept as they are.
\copy gazette_annex_line (line) FROM 'gazette_annex.jsonl' WITH (FORMAT csv, QUOTE E'\x01', DELIMITER E'\x02')
INSERT INTO gazette_annex
SELECT j.law_id,
       CASE j.kind WHEN 'table_row' THEN 'annex-' || j.category || '-' || j.serial ELSE 'original-' || j.number END,
       j.kind, j.block, j.category, j.table_title, j.serial, j.article, j.extra_articles, j.violation, j.points, j.notes,
       j.fine_min, j.fine_max, j.number, j.text_ar, j.gazette_issue, j.gazette_date, j.gazette_pages, j.source_url,
       j.replaced_by_law_id, j.replaced_by_article, j.decided_by, j.readings, j.read_on
  FROM (SELECT (d->>'law_id')::int AS law_id, d->>'kind' AS kind, d->>'block' AS block, d->>'category' AS category,
               d->>'table' AS table_title, (d->>'serial')::int AS serial, d->>'article' AS article,
               d->>'extra_articles' AS extra_articles, d->>'violation' AS violation, d->>'points' AS points,
               d->>'notes' AS notes, (d->>'fine_min')::bigint AS fine_min, (d->>'fine_max')::bigint AS fine_max,
               d->>'number' AS number, d->>'text' AS text_ar, d->>'gazette_issue' AS gazette_issue,
               (d->>'gazette_date')::date AS gazette_date, coalesce(d->>'gazette_page', d->>'gazette_pages') AS gazette_pages,
               d->>'source_url' AS source_url, (d->'replaced_by'->>'law_id')::int AS replaced_by_law_id,
               d->'replaced_by'->>'article' AS replaced_by_article, d->>'decided_by' AS decided_by,
               nullif(d->'readings', 'null'::jsonb) AS readings, (d->>'read_on')::date AS read_on
          FROM (SELECT line::jsonb AS d FROM gazette_annex_line WHERE btrim(line) <> '') x) j
 WHERE EXISTS (SELECT 1 FROM legislation l WHERE l.id = j.law_id)
   AND (j.kind = 'table_row' AND j.category <> '' AND j.serial > 0 AND j.violation <> ''
        OR j.kind = 'article_original' AND j.number <> '' AND j.text_ar <> '');
