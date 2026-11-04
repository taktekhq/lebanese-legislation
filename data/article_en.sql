-- The unofficial English translation of the articles of popular texts,
-- from article_en.jsonl (one JSON object per line: law_id, position, number,
-- source_sha256, en, model, translated_on), made by cmd/translate-en with
-- Gemini. A line is kept only while the article's Arabic text is the one it
-- was made from (source_sha256 of text_ar). The Arabic is the official text.
CREATE TABLE article_en (
  law_id         integer NOT NULL,
  position       integer NOT NULL,
  number         text NOT NULL,
  source_sha256  text NOT NULL,
  en             text NOT NULL,
  model          text NOT NULL,
  translated_on  date NOT NULL,
  PRIMARY KEY (law_id, position),
  FOREIGN KEY (law_id, position) REFERENCES legislation_article (law_id, position)
);
CREATE TEMP TABLE article_en_line (line text);
-- csv with quote and delimiter bytes that never occur: one line, one value,
-- backslashes kept as they are.
\copy article_en_line (line) FROM 'article_en.jsonl' WITH (FORMAT csv, QUOTE E'\x01', DELIMITER E'\x02')
INSERT INTO article_en
SELECT DISTINCT ON (j.law_id, j.position) j.law_id, j.position, j.number, j.source_sha256, j.en, j.model, j.translated_on
  FROM (SELECT (line::jsonb->>'law_id')::int AS law_id, (line::jsonb->>'position')::int AS position,
               line::jsonb->>'number' AS number, line::jsonb->>'source_sha256' AS source_sha256,
               line::jsonb->>'en' AS en, line::jsonb->>'model' AS model, (line::jsonb->>'translated_on')::date AS translated_on
          FROM article_en_line WHERE btrim(line) <> '') j
  JOIN legislation_article a ON a.law_id = j.law_id AND a.position = j.position
 WHERE j.en <> '' AND encode(sha256(convert_to(a.text_ar, 'UTF8')), 'hex') = j.source_sha256
 ORDER BY j.law_id, j.position, j.translated_on DESC;
