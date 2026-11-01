-- ============================================================================
-- Lebanese Legislation: the relational corpus (PostgreSQL 16+)
--
-- Every row comes from a page of the Lebanese University's Centre for Research
-- and Studies in Legal Informatics (legallaw.ul.edu.lb), kept byte for byte in
-- git under archive/. Primary keys are the Centre's own ids wherever it has
-- them, so a row can always be traced to its page, and a rebuild from the same
-- pages produces the same keys (which keeps the git export's history clean).
--
-- Conventions: singular table names; Arabic text as printed (search folds
-- spelling only through indexes, never in stored text); dates the Centre
-- leaves as 01/01/1900 or 01/01/0001 are NULL; "as printed" columns keep the
-- Centre's own wording next to any value we parsed out of it.
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Search-only Arabic folding, never used for display. Two strengths:
--   ar_norm:  drops tashkeel and tatweel, folds أ إ آ ٱ to ا, ى to ي, ة to ه,
--             so one spelling finds every other. For exact-word matching.
--   ar_light: drops tashkeel and tatweel, folds only the alef forms. For the
--             Arabic stemmer, which needs ة and ى to strip suffixes: after the
--             full fold المدنية stems to مدنيه instead of مدن and stops
--             matching المدني.
-- The class is U+064B-U+065F (harakat, tanwin, shadda, sukun), U+0670
-- (dagger alef) and U+0640 (tatweel), written as escapes the regex reads.
CREATE FUNCTION ar_norm(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[\u064B-\u065F\u0670\u0640]', '', 'g'), 'أإآٱىة', 'اااايه');

CREATE FUNCTION ar_light(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[\u064B-\u065F\u0670\u0640]', '', 'g'), 'أإآٱ', 'اااا');

-- Digits for the machine readings of scanned judgments, which print Arabic-Indic
-- numerals (٢٧١ / ٢٠١٤): folded to 0-9 so «271» finds them. Applied inside the
-- vector and the query of the judgment search, before ar_light.
CREATE FUNCTION ar_digits(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(t, '٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹', '01234567890123456789');

-- ---------------------------------------------------------------------------
-- Vocabularies: the Centre's own lists, with English and French names added
-- ---------------------------------------------------------------------------

-- The 49 kinds of text in the Centre's legislation search (قانون، مرسوم، قرار …).
CREATE TABLE legislation_kind (
  name_ar    text PRIMARY KEY,            -- as the Centre prints it
  centre_id  integer UNIQUE,              -- its id in the Centre's search form, when listed
  name_en    text,
  name_fr    text
);

-- The 12 courts and chambers in the Centre's rulings search.
CREATE TABLE court (
  name_ar    text PRIMARY KEY,
  centre_id  integer UNIQUE,
  name_en    text,
  name_fr    text
);


-- ---------------------------------------------------------------------------
-- The Official Gazette (الجريدة الرسمية)
-- ---------------------------------------------------------------------------

CREATE TABLE gazette_issue (
  id            integer PRIMARY KEY,      -- the Centre's Journal id
  number        text,                     -- issue number, as printed on the texts it carries
  published_on  date
);


-- ---------------------------------------------------------------------------
-- Legislation: laws, decrees, decisions … and their chapters and articles
-- ---------------------------------------------------------------------------

CREATE TABLE legislation (
  id                integer PRIMARY KEY,  -- the Centre's LawID
  kind              text NOT NULL REFERENCES legislation_kind (name_ar),
  number            text,                 -- as printed
  number_int        integer,              -- its leading integer, for sorting and lookup
  issued_on         date,
  title             text NOT NULL,
  status            text NOT NULL CHECK (status IN ('in_force', 'repealed')),
  gazette_number    text,                 -- publication, as printed on the text's own page
  gazette_date      date,
  gazette_pages     text,
  preamble          text,                 -- the "based on / whereas" recitals
  explanatory_note  text,                 -- الأسباب الموجبة and similar
  closing           text                  -- promulgation clause, place, date, signatures
);

-- A gazette issue's table of contents: which texts it published, on which page.
CREATE TABLE gazette_entry (
  issue_id  integer NOT NULL REFERENCES gazette_issue (id),
  law_id    integer NOT NULL REFERENCES legislation (id),
  pages     text,
  PRIMARY KEY (issue_id, law_id)
);

-- Texts the Centre has not published yet, read from scans of the Gazette's
-- own pages (internal/build/scans.go). Their ids start at 2,000,000,000.
-- Two machine readings were merged word by word; where they differ, the
-- article text keeps both in a marked span: U+E000 reading one, U+E001
-- reading two, U+E002. The page images are served at /scan/<law_id>/.
CREATE TABLE legislation_scan (
  law_id      integer PRIMARY KEY REFERENCES legislation (id),
  source      text NOT NULL,              -- where the scans came from: saderlex
  source_id   integer NOT NULL,           -- the text's id there
  source_url  text NOT NULL,
  pages       smallint NOT NULL,          -- page images
  agreement   real,                       -- share of words both readings agree on
  flagged     integer                     -- marked spans where they differ
);

-- Dates the Centre printed wrong, and what we show instead (db/date_corrections.tsv,
-- internal/build/dates.go). legislation holds the corrected value; this keeps
-- the printed one and the evidence. A text signed after its own publication
-- that no correction covers gets its signing date set to unknown here too.
CREATE TABLE date_correction (
  law_id     integer NOT NULL REFERENCES legislation (id),
  field      text NOT NULL CHECK (field IN ('issued_on', 'gazette_date')),
  printed    date,                        -- as the Centre prints it
  corrected  date,                        -- what legislation now holds; NULL: not known
  method     text NOT NULL,               -- signature, gazette-issue, year-typo, placeholder, model, guard
  reason     text NOT NULL,               -- the evidence, in a sentence
  PRIMARY KEY (law_id, field)
);

-- Unofficial English titles of the subject line (db/titles_en.tsv, internal/build/titles_en.go),
-- written by a model from the Arabic title alone and shown as such. The Arabic
-- title stays the official one; the law's text is never translated.
CREATE TABLE legislation_title_en (
  law_id    integer PRIMARY KEY REFERENCES legislation (id),
  title_en  text NOT NULL,
  model     text NOT NULL                 -- which model wrote it
);

-- Errors in the Centre's own text of an article (db/centre_errors.tsv),
-- shown next to the article; the text itself stays as the Centre has it.
CREATE TABLE centre_error (
  law_id   integer NOT NULL REFERENCES legislation (id),
  number   text NOT NULL,                 -- the article, as legislation_article.number
  note_en  text NOT NULL,
  note_ar  text NOT NULL,
  PRIMARY KEY (law_id, number)
);

-- The Constitutional Council's decisions on a text (db/council_rulings.tsv):
-- one row for the decision on the text as a whole (number ''), one for each
-- article it annulled. Shown next to them; the wording stays as published.
CREATE TABLE council_ruling (
  law_id      integer NOT NULL REFERENCES legislation (id),
  number      text NOT NULL,              -- the article, as legislation_article.number; '' for the text
  effect      text NOT NULL CHECK (effect IN ('annulled', 'annulled_in_part', 'not_in_force', 'suspended', 'upheld')),
  decision    text NOT NULL,              -- the Council's number for it, as "5/2025"
  decided_on  date NOT NULL,
  source_url  text NOT NULL,
  note_en     text NOT NULL,
  note_ar     text NOT NULL,
  PRIMARY KEY (law_id, number, decision)
);

-- The old address of a scanned text the Centre has since published.
CREATE TABLE legislation_alias (
  id      integer PRIMARY KEY,            -- the scanned text's id
  law_id  integer NOT NULL REFERENCES legislation (id)
);

-- Books, parts, chapters and sections (كتاب، باب، فصل، قسم …), nested.
CREATE TABLE legislation_section (
  id         integer PRIMARY KEY,         -- the Centre's LawTreeSectionID
  law_id     integer NOT NULL REFERENCES legislation (id),
  parent_id  integer REFERENCES legislation_section (id),
  position   integer NOT NULL,            -- order within the text, from 1
  depth      smallint NOT NULL,           -- 0 = top level
  heading    text NOT NULL,
  note       text,                        -- the Centre's editorial note on it ("اضيف هذا الجزء … بموجب …")
  UNIQUE (law_id, position)
);

-- One row per article, in the consolidated text as the Centre keeps it.
CREATE TABLE legislation_article (
  law_id         integer NOT NULL REFERENCES legislation (id),
  position       integer NOT NULL,        -- order within the text, from 1
  centre_id      integer UNIQUE,          -- the Centre's LawArticleID, once its page is fetched
  section_id     integer REFERENCES legislation_section (id),
  number         text NOT NULL,           -- as printed after المادة: '2', '12 مكرر', 'وحيدة'
  number_int     integer,                 -- its leading integer
  note           text,                    -- printed beside the number: (عدلت بموجب القانون 13/2021)
  text_ar        text NOT NULL,
  text_fr        text,                    -- the French text, where one was published
  images         text[],                  -- pictures of the text where the Centre has no typed text
  in_force_from  date,                    -- تاريخ بدء العمل, from the article's own page
  -- Where text_ar and text_fr were read, written with them: ul with
  -- lawtext/<law id>, or saderlex with the scan's id. The wording guard
  -- (internal/provenance) checks the text against that archived source
  -- before any export. Internal: export, the D1 snapshot and the Hugging
  -- Face dataset leave these two columns out.
  source_kind    text,                    -- internal: provenance
  source_id      text,                    -- internal: provenance
  -- Search vectors, computed by Postgres from the text above (never exported):
  -- exact words over the fully folded text, stems over the lightly folded one.
  search_exact   tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(text_ar))) STORED,
  search_stem    tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(text_ar))) STORED,
  search_fr      tsvector GENERATED ALWAYS AS (to_tsvector('french', coalesce(text_fr, ''))) STORED,
  PRIMARY KEY (law_id, position)
);

-- The principal codes, curated: short English and French names and a stable
-- address (/code/obligations-and-contracts/) for the texts people cite most.
CREATE TABLE code (
  slug      text PRIMARY KEY,
  law_id    integer NOT NULL UNIQUE REFERENCES legislation (id),
  name_en   text NOT NULL,
  name_fr   text,
  position  smallint NOT NULL
);



-- ---------------------------------------------------------------------------
-- Case law, opinions, treaties, doctrine
-- ---------------------------------------------------------------------------

CREATE TABLE ruling (
  id          integer PRIMARY KEY,        -- the Centre's ruling id
  court       text REFERENCES court (name_ar),
  number      text,
  year        smallint,
  decided_on  date,
  title       text NOT NULL,              -- as printed: تمييز جزائي حكم رقم : 244 /2019
  president   text,
  members     text[],                     -- the panel, in printed order
  text_ar     text,                       -- NULL when the Centre holds only the judgment's file
  search_exact tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(title || ' ' || coalesce(text_ar, '')))) STORED,
  search_stem  tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(title || ' ' || coalesce(text_ar, '')))) STORED
);

-- The full judgment behind a ruling page («نص الحكم كاملا»): a scanned PDF the
-- Centre serves at RulingFile.aspx?RuliID=N, kept outside git and served at
-- /ruling-files/<ruling_id>.pdf (`ruling-files`, loaded by
-- `build-db -ruling-files`). The scan is the reference.
CREATE TABLE ruling_file (
  ruling_id   integer PRIMARY KEY REFERENCES ruling (id),
  pages       smallint,                   -- NULL until the scan has been read
  bytes       integer NOT NULL,           -- size of the PDF
  sha256      text NOT NULL,              -- of the PDF's bytes
  fetched_at  timestamptz NOT NULL
);

-- A machine reading (Gemini) of the scan, one row per page (`ruling-read`).
-- It may contain errors: nothing here is corrected or rewritten, and the scan
-- stays the reference. Pages the reader could not read are not loaded.
-- search_stem is for finding a ruling by words only its judgment holds.
CREATE TABLE ruling_page (
  ruling_id   integer NOT NULL REFERENCES ruling (id),
  n           smallint NOT NULL,          -- page number in the PDF, from 1
  text_ar     text NOT NULL,
  model       text NOT NULL,              -- the model that read it
  read_at     timestamptz NOT NULL,
  low_confidence boolean NOT NULL DEFAULT false, -- hard to read (mostly handwriting): expect errors
  search_stem tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(ar_digits(text_ar)))) STORED,
  PRIMARY KEY (ruling_id, n)
);

-- Advisory opinions of the Legislation and Consultations Committee and others.
CREATE TABLE opinion (
  id           integer PRIMARY KEY,
  number       text,
  year         smallint,
  issued_on    date,
  requester    text,                      -- the body that asked: وزارة المالية
  file_number  text,
  file_year    smallint,
  subject      text,
  text_ar      text
);

CREATE TABLE agreement (
  id                integer PRIMARY KEY,
  title             text NOT NULL,
  scope             text CHECK (scope IN ('bilateral', 'international')),
  signed_on         date,
  ratified_by       text,                 -- as printed: مرسوم رقم 197 سنة 2025
  ratifying_law_id  integer REFERENCES legislation (id),
  text_ar           text
);

-- Articles and studies from legal periodicals, with their abstracts.
CREATE TABLE doctrine_article (
  id        integer PRIMARY KEY,
  title     text NOT NULL,
  authors   text[],
  series    text,                         -- the periodical, when the Centre gives it
  pages     text,
  abstract  text
);

CREATE TABLE research (
  id            integer PRIMARY KEY,
  title         text NOT NULL,
  author        text,
  published_on  date,
  text_ar       text
);


-- ---------------------------------------------------------------------------
-- References: every place a document points at a text, and how we know it
-- ---------------------------------------------------------------------------

-- One row per reference from a document to a text or one of its articles.
-- `method` says where the row came from, so a guess is never mistaken for a
-- link the Centre's editors made:
--   centre   the Centre's own structured link: a ruling's cited-laws list, the
--            block on a repealed law naming the text that repealed it, the
--            per-law related-documents pages
--   pattern  read from the text by rule ("المادة 708 أ.م.م", "المرسوم رقم 5
--            تاريخ 1/2/2000") and resolved against this database
--   model    resolved by an AI model where the rules could not; kept apart
-- law_id is NULL when the text referred to is not in the corpus; the label
-- still records it. from_id has no foreign key: it points into the table
-- named by from_type.
CREATE TABLE reference (
  from_type          text NOT NULL CHECK (from_type IN
                       ('legislation', 'ruling', 'opinion', 'agreement', 'research', 'doctrine_article')),
  from_id            integer NOT NULL,
  relation           text NOT NULL CHECK (relation IN ('cites', 'based_on', 'amends', 'repeals')),
  law_id             integer REFERENCES legislation (id),
  article_number     text,                -- as referred to; NULL = the text as a whole
  article_centre_id  integer,             -- the Centre's LawArticleID, when its link names one
  label              text,                -- the words on the page
  method             text NOT NULL CHECK (method IN ('centre', 'pattern', 'model')),
  UNIQUE NULLS NOT DISTINCT (from_type, from_id, relation, law_id, article_number, article_centre_id, label, method)
);


-- ---------------------------------------------------------------------------
-- Provenance: the raw page behind every row
-- ---------------------------------------------------------------------------

CREATE TABLE source_page (
  doc_type      text NOT NULL,            -- law, lawtext, lawarticle, ruling, journal …
  centre_id     integer NOT NULL,
  url           text NOT NULL,            -- where the Centre serves it
  archive_path  text NOT NULL,            -- archive/law/0244/244226.html in the git repo
  sha256        text NOT NULL,            -- of the raw bytes, to verify against git
  first_seen    timestamptz NOT NULL,
  last_changed  timestamptz NOT NULL,
  PRIMARY KEY (doc_type, centre_id)
);


-- Pages search engines are asked to skip: `noindex, follow` on the page and no
-- entry in the sitemaps. The page stays reachable and linked. Written by the
-- build (internal/indexing): an article whose text is under 25 words and only
-- a publication, commencement, notification or repeal clause (or is empty), a
-- document with a placeholder title or no text.
CREATE TABLE noindex_page (
  kind      text NOT NULL CHECK (kind IN ('article', 'ruling', 'opinion', 'agreement', 'doctrine', 'research')),
  id        integer NOT NULL,             -- the text's id for an article, else the document's id
  position  integer NOT NULL DEFAULT 0,   -- an article's position in its text; 0 for a document
  reason    text NOT NULL,                -- empty, boilerplate, placeholder
  PRIMARY KEY (kind, id, position)
);

-- When a page's content last changed, for the sitemaps' lastmod. content_hash
-- is a hash of what the page shows; changed_at moves only when it changes, and
-- the build carries both across rebuilds. A page seen for the first time gets
-- the day it first existed here (its source first fetched) or a later change:
-- an amendment coming into force, a judgment reading added, a change the
-- scraper saw at the Centre. Never the text's own date: a 1964 decree's page
-- did not change in 1964 (internal/build/indexing.go).
CREATE TABLE page_state (
  kind          text NOT NULL CHECK (kind IN ('law', 'ruling', 'gazette', 'opinion', 'agreement', 'doctrine', 'research')),
  id            integer NOT NULL,
  content_hash  text NOT NULL,
  changed_at    timestamptz NOT NULL,
  PRIMARY KEY (kind, id)
);


-- ---------------------------------------------------------------------------
-- Views for everyday questions
-- ---------------------------------------------------------------------------

-- Each text with its size, its gazette issue and whether its text is in yet.
CREATE VIEW legislation_summary AS
SELECT l.*,
       (SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id) AS article_count,
       (SELECT min(e.issue_id) FROM gazette_entry e WHERE e.law_id = l.id) AS gazette_issue_id,
       (SELECT min(r.from_id) FROM reference r WHERE r.law_id = l.id
          AND r.from_type = 'legislation' AND r.relation = 'repeals') AS repealed_by
FROM legislation l;

-- Each article with the identity of its text, ready to cite.
CREATE VIEW article_cited AS
SELECT a.*, l.kind, l.number AS law_number, l.issued_on, l.title AS law_title, l.status,
       'المادة ' || a.number || ' من ' || l.title AS citation
FROM legislation_article a JOIN legislation l ON l.id = a.law_id;
