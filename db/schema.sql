
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE FUNCTION ar_norm(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[\u064B-\u065F\u0670\u0640]', '', 'g'), 'أإآٱىة', 'اااايه');

CREATE FUNCTION ar_light(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(regexp_replace(t, '[\u064B-\u065F\u0670\u0640]', '', 'g'), 'أإآٱ', 'اااا');

CREATE FUNCTION ar_digits(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
RETURN translate(t, '٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹', '01234567890123456789');


CREATE TABLE legislation_kind (
  name_ar    text PRIMARY KEY,            
  centre_id  integer UNIQUE,              
  name_en    text,
  name_fr    text
);

CREATE TABLE court (
  name_ar    text PRIMARY KEY,
  centre_id  integer UNIQUE,
  name_en    text,
  name_fr    text
);



CREATE TABLE gazette_issue (
  id            integer PRIMARY KEY,      
  number        text,                     
  published_on  date
);



CREATE TABLE legislation (
  id                integer PRIMARY KEY,  
  kind              text NOT NULL REFERENCES legislation_kind (name_ar),
  number            text,                 
  number_int        integer,              
  issued_on         date,
  title             text NOT NULL,
  status            text NOT NULL CHECK (status IN ('in_force', 'repealed')),
  gazette_number    text,                 
  gazette_date      date,
  gazette_pages     text,
  preamble          text,                 
  explanatory_note  text,                 
  closing           text                  
);

CREATE TABLE gazette_entry (
  issue_id  integer NOT NULL REFERENCES gazette_issue (id),
  law_id    integer NOT NULL REFERENCES legislation (id),
  pages     text,
  PRIMARY KEY (issue_id, law_id)
);

CREATE TABLE legislation_scan (
  law_id      integer PRIMARY KEY REFERENCES legislation (id),
  source      text NOT NULL,              
  source_id   integer NOT NULL,           
  source_url  text NOT NULL,
  pages       smallint NOT NULL,          
  agreement   real,                       
  flagged     integer                     
);

CREATE TABLE date_correction (
  law_id     integer NOT NULL REFERENCES legislation (id),
  field      text NOT NULL CHECK (field IN ('issued_on', 'gazette_date')),
  printed    date,                        
  corrected  date,                        
  method     text NOT NULL,               
  reason     text NOT NULL,               
  PRIMARY KEY (law_id, field)
);

CREATE TABLE legislation_title_en (
  law_id    integer PRIMARY KEY REFERENCES legislation (id),
  title_en  text NOT NULL,
  model     text NOT NULL                 
);

CREATE TABLE centre_error (
  law_id   integer NOT NULL REFERENCES legislation (id),
  number   text NOT NULL,                 
  note_en  text NOT NULL,
  note_ar  text NOT NULL,
  PRIMARY KEY (law_id, number)
);

CREATE TABLE council_ruling (
  law_id      integer NOT NULL REFERENCES legislation (id),
  number      text NOT NULL,              
  effect      text NOT NULL CHECK (effect IN ('annulled', 'annulled_in_part', 'not_in_force', 'suspended', 'upheld')),
  decision    text NOT NULL,              
  decided_on  date NOT NULL,
  source_url  text NOT NULL,
  note_en     text NOT NULL,
  note_ar     text NOT NULL,
  PRIMARY KEY (law_id, number, decision)
);

CREATE TABLE legislation_alias (
  id      integer PRIMARY KEY,            
  law_id  integer NOT NULL REFERENCES legislation (id)
);

CREATE TABLE legislation_section (
  id         integer PRIMARY KEY,         
  law_id     integer NOT NULL REFERENCES legislation (id),
  parent_id  integer REFERENCES legislation_section (id),
  position   integer NOT NULL,            
  depth      smallint NOT NULL,           
  heading    text NOT NULL,
  note       text,                        
  UNIQUE (law_id, position)
);

CREATE TABLE legislation_article (
  law_id         integer NOT NULL REFERENCES legislation (id),
  position       integer NOT NULL,        
  centre_id      integer UNIQUE,          
  section_id     integer REFERENCES legislation_section (id),
  number         text NOT NULL,           
  number_int     integer,                 
  note           text,                    
  text_ar        text NOT NULL,
  text_fr        text,                    
  images         text[],                  
  in_force_from  date,                    
  source_kind    text,                    
  source_id      text,                    
  search_exact   tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(text_ar))) STORED,
  search_stem    tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(text_ar))) STORED,
  search_fr      tsvector GENERATED ALWAYS AS (to_tsvector('french', coalesce(text_fr, ''))) STORED,
  PRIMARY KEY (law_id, position)
);

CREATE TABLE code (
  slug      text PRIMARY KEY,
  law_id    integer NOT NULL UNIQUE REFERENCES legislation (id),
  name_en   text NOT NULL,
  name_fr   text,
  position  smallint NOT NULL
);




CREATE TABLE ruling (
  id          integer PRIMARY KEY,        
  court       text REFERENCES court (name_ar),
  number      text,
  year        smallint,
  decided_on  date,
  title       text NOT NULL,              
  president   text,
  members     text[],                     
  text_ar     text,                       
  search_exact tsvector GENERATED ALWAYS AS (to_tsvector('simple', ar_norm(title || ' ' || coalesce(text_ar, '')))) STORED,
  search_stem  tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(title || ' ' || coalesce(text_ar, '')))) STORED
);

CREATE TABLE ruling_file (
  ruling_id   integer PRIMARY KEY REFERENCES ruling (id),
  pages       smallint,                   
  bytes       integer NOT NULL,           
  sha256      text NOT NULL,              
  fetched_at  timestamptz NOT NULL
);

CREATE TABLE ruling_page (
  ruling_id   integer NOT NULL REFERENCES ruling (id),
  n           smallint NOT NULL,          
  text_ar     text NOT NULL,
  model       text NOT NULL,              
  read_at     timestamptz NOT NULL,
  low_confidence boolean NOT NULL DEFAULT false, 
  search_stem tsvector GENERATED ALWAYS AS (to_tsvector('arabic', ar_light(ar_digits(text_ar)))) STORED,
  PRIMARY KEY (ruling_id, n)
);

CREATE TABLE opinion (
  id           integer PRIMARY KEY,
  number       text,
  year         smallint,
  issued_on    date,
  requester    text,                      
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
  ratified_by       text,                 
  ratifying_law_id  integer REFERENCES legislation (id),
  text_ar           text
);

CREATE TABLE doctrine_article (
  id        integer PRIMARY KEY,
  title     text NOT NULL,
  authors   text[],
  series    text,                         
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



CREATE TABLE reference (
  from_type          text NOT NULL CHECK (from_type IN
                       ('legislation', 'ruling', 'opinion', 'agreement', 'research', 'doctrine_article')),
  from_id            integer NOT NULL,
  relation           text NOT NULL CHECK (relation IN ('cites', 'based_on', 'amends', 'repeals')),
  law_id             integer REFERENCES legislation (id),
  article_number     text,                
  article_centre_id  integer,             
  label              text,                
  method             text NOT NULL CHECK (method IN ('centre', 'pattern', 'model')),
  UNIQUE NULLS NOT DISTINCT (from_type, from_id, relation, law_id, article_number, article_centre_id, label, method)
);



CREATE TABLE source_page (
  doc_type      text NOT NULL,            
  centre_id     integer NOT NULL,
  url           text NOT NULL,            
  last_changed  timestamptz NOT NULL,
  PRIMARY KEY (doc_type, centre_id)
);


CREATE TABLE noindex_page (
  kind      text NOT NULL CHECK (kind IN ('article', 'ruling', 'opinion', 'agreement', 'doctrine', 'research')),
  id        integer NOT NULL,             
  position  integer NOT NULL DEFAULT 0,   
  reason    text NOT NULL,                
  PRIMARY KEY (kind, id, position)
);

CREATE TABLE page_state (
  kind          text NOT NULL CHECK (kind IN ('law', 'ruling', 'gazette', 'opinion', 'agreement', 'doctrine', 'research')),
  id            integer NOT NULL,
  content_hash  text NOT NULL,
  changed_at    timestamptz NOT NULL,
  PRIMARY KEY (kind, id)
);



CREATE VIEW legislation_summary AS
SELECT l.*,
       (SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id) AS article_count,
       (SELECT min(e.issue_id) FROM gazette_entry e WHERE e.law_id = l.id) AS gazette_issue_id,
       (SELECT min(r.from_id) FROM reference r WHERE r.law_id = l.id
          AND r.from_type = 'legislation' AND r.relation = 'repeals') AS repealed_by
FROM legislation l;

CREATE VIEW article_cited AS
SELECT a.*, l.kind, l.number AS law_number, l.issued_on, l.title AS law_title, l.status,
       'المادة ' || a.number || ' من ' || l.title AS citation
FROM legislation_article a JOIN legislation l ON l.id = a.law_id;