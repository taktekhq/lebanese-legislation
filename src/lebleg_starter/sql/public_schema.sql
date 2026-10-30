-- Public snapshot schema. Views are materialized tables; search and private provenance omitted.
CREATE TABLE agreement (
 "id" integer,
 "title" text,
 "scope" text,
 "signed_on" date,
 "ratified_by" text,
 "ratifying_law_id" integer,
 "text_ar" text
);
CREATE TABLE amendment (
 "id" integer,
 "by_law_id" integer,
 "by_article" text,
 "seq" smallint,
 "action" text,
 "law_id" integer,
 "article_number" text,
 "part" text,
 "text_ar" text,
 "old_text_ar" text,
 "effective_on" date,
 "status" text,
 "reason" text,
 "method" text,
 "resolved_by" text,
 "judged_by" text,
 "judgement" text,
 "evidence" text
);
CREATE TABLE article_in_force (
 "law_id" integer,
 "number" text,
 "position" integer,
 "state" text,
 "text_ar" text,
 "version" text,
 "text_source" text,
 "valid_from" date,
 "amendment_id" integer,
 "pending" boolean
);
CREATE TABLE article_version (
 "law_id" integer,
 "article_number" text,
 "major" smallint,
 "minor" smallint,
 "valid_from" date,
 "valid_to" date,
 "state" text,
 "text_ar" text,
 "text_source" text,
 "amendment_id" integer,
 "note" text
);
CREATE TABLE centre_error (
 "law_id" integer,
 "number" text,
 "note_en" text,
 "note_ar" text
);
CREATE TABLE code (
 "slug" text,
 "law_id" integer,
 "name_en" text,
 "name_fr" text,
 "position" smallint
);
CREATE TABLE court (
 "name_ar" text,
 "centre_id" integer,
 "name_en" text,
 "name_fr" text
);
CREATE TABLE date_correction (
 "law_id" integer,
 "field" text,
 "printed" date,
 "corrected" date,
 "method" text,
 "reason" text
);
CREATE TABLE doctrine_article (
 "id" integer,
 "title" text,
 "authors" text[],
 "series" text,
 "pages" text,
 "abstract" text
);
CREATE TABLE gazette_entry (
 "issue_id" integer,
 "law_id" integer,
 "pages" text
);
CREATE TABLE gazette_issue (
 "id" integer,
 "number" text,
 "published_on" date
);
CREATE TABLE graph_edge (
 "id" integer,
 "from_key" text,
 "to_key" text,
 "relation" text,
 "source" text,
 "status" text,
 "from_type" text,
 "from_id" integer,
 "from_article" text,
 "to_law_id" integer,
 "to_article" text,
 "to_cite" text,
 "part" text,
 "how" text,
 "quote" text,
 "model" text
);
CREATE TABLE graph_node (
 "key" text,
 "type" text,
 "id" integer,
 "article" text,
 "label" text,
 "title" text,
 "dated" date,
 "status" text
);
CREATE TABLE legislation (
 "id" integer,
 "kind" text,
 "number" text,
 "number_int" integer,
 "issued_on" date,
 "title" text,
 "status" text,
 "gazette_number" text,
 "gazette_date" date,
 "gazette_pages" text,
 "preamble" text,
 "explanatory_note" text,
 "closing" text,
 "title_en" text
);
CREATE TABLE legislation_alias (
 "id" integer,
 "law_id" integer
);
CREATE TABLE legislation_article (
 "law_id" integer,
 "position" integer,
 "centre_id" integer,
 "section_id" integer,
 "number" text,
 "number_int" integer,
 "note" text,
 "text_ar" text,
 "text_fr" text,
 "images" text[],
 "in_force_from" date
);
CREATE TABLE legislation_kind (
 "name_ar" text,
 "centre_id" integer,
 "name_en" text,
 "name_fr" text
);
CREATE TABLE legislation_scan (
 "law_id" integer,
 "source" text,
 "source_id" integer,
 "source_url" text,
 "pages" smallint,
 "agreement" real,
 "flagged" integer
);
CREATE TABLE legislation_section (
 "id" integer,
 "law_id" integer,
 "parent_id" integer,
 "position" integer,
 "depth" smallint,
 "heading" text,
 "note" text
);
CREATE TABLE legislation_title_en (
 "law_id" integer,
 "title_en" text,
 "model" text
);
CREATE TABLE legislation_version (
 "law_id" integer,
 "version" integer,
 "valid_from" date,
 "amended_by" integer[],
 "changes" integer
);
CREATE TABLE opinion (
 "id" integer,
 "number" text,
 "year" smallint,
 "issued_on" date,
 "requester" text,
 "file_number" text,
 "file_year" smallint,
 "subject" text,
 "text_ar" text
);
CREATE TABLE possibly_affected (
 "repealing_law_id" integer,
 "repealing_article" text,
 "law_id" integer,
 "article_number" text
);
CREATE TABLE reference (
 "from_type" text,
 "from_id" integer,
 "relation" text,
 "law_id" integer,
 "article_number" text,
 "article_centre_id" integer,
 "label" text,
 "method" text
);
CREATE TABLE research (
 "id" integer,
 "title" text,
 "author" text,
 "published_on" date,
 "text_ar" text
);
CREATE TABLE ruling (
 "id" integer,
 "court" text,
 "number" text,
 "year" smallint,
 "decided_on" date,
 "title" text,
 "president" text,
 "members" text[],
 "text_ar" text
);
CREATE TABLE ruling_file (
 "ruling_id" integer,
 "pages" smallint,
 "bytes" integer,
 "sha256" text,
 "fetched_at" timestamp with time zone
);
CREATE TABLE ruling_page (
 "ruling_id" integer,
 "n" smallint,
 "text_ar" text,
 "model" text,
 "read_at" timestamp with time zone,
 "low_confidence" boolean
);
CREATE TABLE source_page (
 "doc_type" text,
 "centre_id" integer,
 "url" text,
 "archive_path" text,
 "sha256" text,
 "first_seen" timestamp with time zone,
 "last_changed" timestamp with time zone
);
ALTER TABLE agreement ADD PRIMARY KEY (id);
ALTER TABLE agreement ADD FOREIGN KEY (ratifying_law_id) REFERENCES legislation(id);
ALTER TABLE amendment ADD PRIMARY KEY (id);
ALTER TABLE amendment ADD UNIQUE (by_law_id, by_article, seq, article_number);
ALTER TABLE amendment ADD FOREIGN KEY (by_law_id) REFERENCES legislation(id);
ALTER TABLE amendment ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE article_version ADD PRIMARY KEY (law_id, article_number, major, minor);
ALTER TABLE article_version ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE article_version ADD FOREIGN KEY (amendment_id) REFERENCES amendment(id);
ALTER TABLE centre_error ADD PRIMARY KEY (law_id, number);
ALTER TABLE centre_error ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE code ADD PRIMARY KEY (slug);
ALTER TABLE code ADD UNIQUE (law_id);
ALTER TABLE code ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE court ADD PRIMARY KEY (name_ar);
ALTER TABLE court ADD UNIQUE (centre_id);
ALTER TABLE date_correction ADD PRIMARY KEY (law_id, field);
ALTER TABLE date_correction ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE doctrine_article ADD PRIMARY KEY (id);
ALTER TABLE gazette_entry ADD PRIMARY KEY (issue_id, law_id);
ALTER TABLE gazette_entry ADD FOREIGN KEY (issue_id) REFERENCES gazette_issue(id);
ALTER TABLE gazette_entry ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE gazette_issue ADD PRIMARY KEY (id);
ALTER TABLE graph_edge ADD PRIMARY KEY (id);
ALTER TABLE legislation ADD PRIMARY KEY (id);
ALTER TABLE legislation ADD FOREIGN KEY (kind) REFERENCES legislation_kind(name_ar);
ALTER TABLE legislation_alias ADD PRIMARY KEY (id);
ALTER TABLE legislation_alias ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE legislation_article ADD PRIMARY KEY (law_id, "position");
ALTER TABLE legislation_article ADD UNIQUE (centre_id);
ALTER TABLE legislation_article ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE legislation_article ADD FOREIGN KEY (section_id) REFERENCES legislation_section(id);
ALTER TABLE legislation_kind ADD PRIMARY KEY (name_ar);
ALTER TABLE legislation_kind ADD UNIQUE (centre_id);
ALTER TABLE legislation_scan ADD PRIMARY KEY (law_id);
ALTER TABLE legislation_scan ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE legislation_section ADD PRIMARY KEY (id);
ALTER TABLE legislation_section ADD UNIQUE (law_id, "position");
ALTER TABLE legislation_section ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE legislation_section ADD FOREIGN KEY (parent_id) REFERENCES legislation_section(id);
ALTER TABLE legislation_title_en ADD PRIMARY KEY (law_id);
ALTER TABLE legislation_title_en ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE opinion ADD PRIMARY KEY (id);
ALTER TABLE reference ADD UNIQUE NULLS NOT DISTINCT (from_type, from_id, relation, law_id, article_number, article_centre_id, label, method);
ALTER TABLE reference ADD FOREIGN KEY (law_id) REFERENCES legislation(id);
ALTER TABLE research ADD PRIMARY KEY (id);
ALTER TABLE ruling ADD PRIMARY KEY (id);
ALTER TABLE ruling ADD FOREIGN KEY (court) REFERENCES court(name_ar);
ALTER TABLE ruling_file ADD PRIMARY KEY (ruling_id);
ALTER TABLE ruling_file ADD FOREIGN KEY (ruling_id) REFERENCES ruling(id);
ALTER TABLE ruling_page ADD PRIMARY KEY (ruling_id, n);
ALTER TABLE ruling_page ADD FOREIGN KEY (ruling_id) REFERENCES ruling(id);
ALTER TABLE source_page ADD PRIMARY KEY (doc_type, centre_id);
