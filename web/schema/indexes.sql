

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

CREATE INDEX opinion_stem   ON opinion USING gin (to_tsvector('arabic', ar_light(coalesce(subject, '') || ' ' || coalesce(text_ar, ''))));
CREATE INDEX gazette_entry_law ON gazette_entry (law_id);
CREATE INDEX reference_to      ON reference (law_id, relation);
CREATE INDEX reference_article ON reference (article_centre_id);

CREATE INDEX IF NOT EXISTS graph_edge_from ON graph_edge (from_key);
CREATE INDEX IF NOT EXISTS graph_edge_to ON graph_edge (to_key);
CREATE INDEX IF NOT EXISTS graph_edge_to_law ON graph_edge (to_law_id);
CREATE INDEX IF NOT EXISTS graph_edge_from_id ON graph_edge (from_type, from_id);

CREATE INDEX ruling_page_stem ON ruling_page USING gin (search_stem);