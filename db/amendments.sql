-- Amendments and article versions: the layer the build derives from the
-- amending articles (internal/amend). Created after schema.sql, and again
-- on its own by "lebleg build -only amendments".

DROP VIEW IF EXISTS legislation_version;
DROP VIEW IF EXISTS article_in_force;
DROP TABLE IF EXISTS article_version;
DROP TABLE IF EXISTS amendment;

-- One change one text made to another, read from the amending article
-- ("تعدل المادة 73 من القانون رقم 328 تاريخ 2 آب 2001 لتصبح …"). The build
-- reads every amending article (method 'pattern'); db/amendments.tsv adds
-- or corrects changes by hand (method 'editorial').
--   status  applied   made a new version of the article (or repealed the text)
--           partial   changed part of the article (a paragraph, a clause): shown
--                     as a note; the full new wording needs a person
--           review    not shown: the text or article it names wasn't found, or
--                     the wording couldn't be read; reason says which
--           rejected  checked by a person and wrong
CREATE TABLE amendment (
  id              integer PRIMARY KEY,
  by_law_id       integer NOT NULL REFERENCES legislation (id),  -- the amending text
  by_article      text NOT NULL,                                -- its article
  seq             smallint NOT NULL,       -- the change's place among the article's changes
  action          text NOT NULL CHECK (action IN ('replace', 'repeal', 'insert')),
  law_id          integer REFERENCES legislation (id),  -- the text changed; NULL when not found
  article_number  text,                    -- the article changed; NULL for the text as a whole
  part            text,                    -- the part changed, as the amendment names it; '+' an addition
  text_ar         text,                    -- the new wording, as published
  old_text_ar     text,                    -- the wording replaced, when the amendment quotes it
  effective_on    date,                    -- publication in the Official Gazette, else the amending text's date
  status          text NOT NULL CHECK (status IN ('applied', 'partial', 'review', 'rejected')),
  reason          text,                    -- why not applied; flags such as "retroactive"
  method          text NOT NULL CHECK (method IN ('pattern', 'editorial', 'model')),
  resolved_by     text,                    -- how the changed text was identified
  judged_by       text,                    -- the model that judged it (db/model/judgements.jsonl), if any
  judgement       text,                    -- its answer: includes, predates, later, unclear
  evidence        text,                    -- the amending article's opening words
  UNIQUE (by_law_id, by_article, seq, article_number)
);

-- Each amended article's versions, oldest first, numbered major.minor.
-- A major version is a legal event: a text changed the article, from a
-- date (1.0 is the earliest wording we have). A minor version rewrites the
-- same legal state without changing its dates: a corrected typing, a
-- person's correction. Only people write minor versions; models don't
-- write the law's wording. The latest version with a wording is the text
-- in force.
-- Articles never amended have no rows: their one version is the Centre's
-- text (see article_in_force).
--   text_source  centre     the Centre's text is this version
--                amendment  the wording the amendment published
--                editorial  written by a person (a correction)
--                none       no source keeps this wording (an earlier version
--                           the Centre overwrote, or a part change not yet
--                           consolidated)
CREATE TABLE article_version (
  law_id          integer NOT NULL REFERENCES legislation (id),
  article_number  text NOT NULL,
  major           smallint NOT NULL,
  minor           smallint NOT NULL,
  valid_from      date,                    -- NULL: from the text's own start, or not known
  valid_to        date,                    -- NULL: still in force, or not known
  state           text NOT NULL CHECK (state IN ('in_force', 'repealed')),
  text_ar         text,
  text_source     text NOT NULL CHECK (text_source IN ('centre', 'amendment', 'editorial', 'none')),
  amendment_id    integer REFERENCES amendment (id),  -- the change that made it; NULL for 1.0
  note            text,                    -- e.g. "a later change not yet identified"
  -- Where text_ar was read, NULL when it has none: the article's own source
  -- (centre), amendment with <amending law id>/<article> (amendment), or
  -- edit with the correction's id (editorial). Checked by the wording guard;
  -- internal, never exported.
  source_kind     text,                    -- internal: provenance
  source_id       text,                    -- internal: provenance
  PRIMARY KEY (law_id, article_number, major, minor)
);

-- Each article's text in force today: the latest version with a wording,
-- or the Centre's text when it was never amended. pending says the latest
-- change has no full wording yet; state is the latest major version's, or
-- repealed when the Centre's own note says so ("مادة ملغاة (الغيت بموجب …)":
-- the Centre empties the text, and its note is newer than any version we
-- read). position is the article's place in the Centre's text, NULL for an
-- article only an amendment carries: a number can appear twice in a law
-- (Penal Code 335, once per section), so join on it.
-- Articles added by an amendment that the Centre doesn't carry appear too.
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

-- Each text's versions: one per date an amendment changed it, after the
-- original (version 1, from its own date).
CREATE VIEW legislation_version AS
SELECT l.id AS law_id, 1 AS version, l.issued_on AS valid_from, NULL::integer[] AS amended_by, 0 AS changes
FROM legislation l
UNION ALL
SELECT law_id, (1 + row_number() OVER (PARTITION BY law_id ORDER BY effective_on))::int,
       effective_on, array_agg(DISTINCT by_law_id ORDER BY by_law_id), count(*)::int
FROM amendment
WHERE status IN ('applied', 'partial') AND law_id IS NOT NULL AND effective_on IS NOT NULL
GROUP BY law_id, effective_on;
