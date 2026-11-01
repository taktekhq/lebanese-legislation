INSERT INTO legislation_kind(name_ar, centre_id, name_en) VALUES ('قانون', 1, 'Law');
INSERT INTO court(name_ar, centre_id, name_en) VALUES ('محكمة التمييز', 1, 'Court of Cassation');
INSERT INTO legislation(id, kind, number, issued_on, title, status, articles, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES
  (7001, 'قانون', '1', '2020-01-01', 'قانون الاختبار', 'in_force', 35, NULL, NULL, NULL, NULL, NULL, NULL);
INSERT INTO legislation_article(law_id, position, number, text_ar)
SELECT 7001, n, n::text, 'نص المادة ' || n FROM generate_series(1, 35) n;
INSERT INTO ruling(id, court, number, year, decided_on, title, president, members, text_ar) VALUES
  (7002, 'محكمة التمييز', '42', 2024, '2024-03-10', 'حكم رقم 42/2024', NULL, '{}', 'تطبيق المادة 28 من القانون');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
SELECT n, 'ruling/7002', 'law/7001/art/' || n::text, 'applies', 'model', 'linked', 'ruling', 7002, 7001, n::text
FROM generate_series(1, 35) n;
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (37, 'ruling/7002', 'law/7001/art/28', 'cites', 'centre', 'linked', 'ruling', 7002, 7001, '28');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (38, 'ruling/7002', 'law/7001/art/34', 'cites', 'centre', 'unresolved', 'ruling', 7002, 7001, '34');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (39, 'ruling/7002', 'law/7001/art/35', 'cites', 'centre', 'no_article', 'ruling', 7002, 7001, '35');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (40, 'ruling/7002', 'law/7001/art/28', 'cites', 'centre', 'linked', 'ruling', 7002, 7001, '28');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (41, 'ruling/7002', 'law/7001/art/28', 'cites', 'centre', 'linked', 'ruling', 7002, 7001, '28');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (42, 'ruling/7002', 'law/7001/art/34', 'cites', 'centre', 'linked', 'ruling', 7002, 7001, '34');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (43, 'ruling/7002', 'law/7001/art/35', 'cites', 'centre', 'linked', 'ruling', 7002, 7001, '35');

-- Avoid repetition in the test setup: the preceding seed is expanded to
-- twenty-five additional, distinct articles for the inline-limit check.
INSERT INTO legislation_article(law_id, position, number, text_ar)
SELECT 7001, n, n::text, 'نص المادة ' || n FROM generate_series(36, 50) n;
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
SELECT n + 43, 'ruling/7002', 'law/7001/art/' || n::text, 'applies', 'model', 'linked', 'ruling', 7002, 7001, n::text
FROM generate_series(36, 50) n;
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (36, 'ruling/7002', 'law/7001', 'applies', 'model', 'linked', 'ruling', 7002, 7001, NULL);
INSERT INTO reference(from_type, from_id, relation, law_id, article_number, label) VALUES ('ruling', 7002, 'cites', 7001, '28', 'المادة 28');
INSERT INTO source_page(doc_type, centre_id, url) VALUES ('law', 7001, 'https://example.test/law/7001');
INSERT INTO source_page(doc_type, centre_id, url) VALUES ('ruling', 7002, 'https://example.test/ruling/7002');
INSERT INTO ruling(id, court, number, year, decided_on, title, president, members, text_ar) VALUES
  (7003, 'محكمة التمييز', '41', 2023, '2023-03-10', 'حكم رقم 41/2023', NULL, '{}', 'حكم أقدم');
INSERT INTO graph_edge(id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
VALUES (44, 'ruling/7003', 'law/7001/art/28', 'cites', 'model', 'linked', 'ruling', 7003, 7001, '28');
