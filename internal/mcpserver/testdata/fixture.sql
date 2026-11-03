-- A tiny corpus for the DB tests, in the real schema (db/schema.sql,
-- db/amendments.sql, db/graph.sql), using the Postgres tables queried
-- by the tools.
--
-- Law 100 (article 5), amending decree 200 (article 1, which amends law
-- 100's article 5), a general-repeal law 300 that possibly affects law 100,
-- code 400 with an amended, a repealed and an added article, three codes
-- whose names share "penal", Law 3/2025 (333455) inserting ten articles
-- into the Labour Code (190374), Law 68/2026 (a Gazette scan) amending
-- Penal Code article 37, a text with no date (600600), rulings, Gazette
-- issues, and the Code of Civil Procedure's article 11 (calculated amounts).

INSERT INTO legislation_kind (name_ar, name_en) VALUES
  ('قانون', 'Law'), ('مرسوم', 'Decree'), ('مرسوم إشتراعى', 'Legislative decree'), ('قرار', 'Decision');
INSERT INTO court (name_ar, name_en) VALUES ('محكمة التمييز', 'Court of Cassation');

INSERT INTO gazette_issue (id, number, published_on) VALUES
  (500, '123', '2001-01-05'), (600, '22', '2025-05-16'), (601, '23', '2025-05-23'), (2002026036, '36', '2026-09-10');

INSERT INTO legislation (id, kind, number, issued_on, title, status, gazette_number, gazette_date) VALUES
  (100, 'قانون', '10', '2001-01-01', 'قانون الاختبار', 'in_force', '123', '2001-01-05'),
  (200, 'مرسوم', '20', '2005-06-01', 'مرسوم التعديل', 'in_force', NULL, NULL),
  (300, 'قانون', '30', '2010-03-01', 'قانون الالغاء العام', 'in_force', NULL, NULL),
  (400, 'قانون', '40', '2001-01-01', 'قانون معدل', 'in_force', NULL, NULL),
  (410, 'قانون', '41', '1946-01-12', 'قانون العقوبات العسكري', 'repealed', NULL, NULL),
  (420, 'قانون', '42', '2001-08-02', 'اصول المحاكمات الجزائية', 'in_force', NULL, NULL),
  (430, 'مرسوم إشتراعى', '43', '1943-03-01', 'قانون العقوبات', 'in_force', NULL, NULL),
  (500, 'قانون', '81', '2018-10-18', 'قانون الجرائم المعلوماتية', 'in_force', NULL, NULL),
  (501, 'قانون', '40', '1999-01-01', 'قانون الزواج المدني', 'in_force', NULL, NULL),
  (502, 'قانون', '2', '1956-09-03', 'قانون السرية المصرفية', 'in_force', NULL, NULL),
  (503, 'مرسوم', '513', '2005-06-01', 'تنظيم هيئة إدارة السير والآليات والمركبات', 'in_force', NULL, NULL),
  (504, 'مرسوم', '88', '2024-03-01', 'تعيين موظف في وزارة الخارجية', 'in_force', NULL, NULL),
  (505, 'مرسوم', '89', '2019-03-01', 'تعيين موظف في وزارة الخارجية', 'in_force', NULL, NULL),
  (506, 'مرسوم', '7', '1959-01-01', 'تعيين اللواء فؤاد شهاب', 'in_force', NULL, NULL),
  (507, 'مرسوم', '9', '2019-02-02', 'منح هبة لمكتب مكافحة الجرائم المعلوماتية', 'in_force', NULL, NULL),
  (278573, 'قانون', '81', '2018-10-10', 'المعاملات الإلكترونية والبيانات ذات الطابع الشخصي', 'in_force', NULL, NULL),
  (190374, 'قانون', '0', '1946-09-23', 'قانون العمل', 'in_force', NULL, NULL),
  (333455, 'قانون', '3', '2025-04-29', 'قانون تعديل قانون العمل', 'in_force', '22', '2025-05-16'),
  (2000200346, 'قانون', '68', '2026-09-01', 'قانون تعديل بعض احكام قانون العقوبات', 'in_force', '36', '2026-09-10'),
  (2000200347, 'مرسوم', '1200', '2026-09-02', 'مرسوم تنظيم', 'in_force', '36', '2026-09-10'),
  (600600, 'قرار', '5', NULL, 'قرار بلا تاريخ', 'in_force', NULL, NULL),
  (244565, 'مرسوم إشتراعى', '90', '1983-09-16', 'قانون اصول المحاكمات المدنية', 'in_force', NULL, NULL);

INSERT INTO gazette_entry (issue_id, law_id) VALUES
  (500, 100), (600, 333455), (2002026036, 2000200346), (2002026036, 2000200347);

INSERT INTO code (slug, law_id, name_en, name_fr, position) VALUES
  ('test-code', 400, 'Test Code', 'Code des essais', 1),
  ('military-penal', 410, 'Military Penal Code', 'Code pénal militaire', 2),
  ('criminal-procedure', 420, 'Code of Criminal Procedure', 'Code de procédure pénale', 3),
  ('penal', 430, 'Penal Code', 'Code pénal', 4),
  ('civil-procedure', 244565, 'Code of Civil Procedure', 'Code de procédure civile', 5);

INSERT INTO source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES
  ('law', 100, 'https://legallaw.ul.edu.lb/Law.aspx?LawID=100', 'archive/law/0000/100.html', 'x', now(), now());

INSERT INTO legislation_title_en (law_id, title_en, model) VALUES
  (100, 'The test law', 'test'), (500, 'Law on information crimes', 'test'),
  (506, 'Appointment of General Fouad Shehab', 'test'), (507, 'Grant to the cybercrime bureau', 'test');

INSERT INTO legislation_section (id, law_id, parent_id, position, depth, heading) VALUES
  (41, 400, NULL, 1, 0, 'الباب الاول :- احكام عامة'), (42, 400, 41, 2, 1, 'الفصل الاول:');

INSERT INTO legislation_article (law_id, position, section_id, number, note, text_ar, text_fr, in_force_from) VALUES
  (100, 1, NULL, '5', NULL, 'نص المادة الخامسة من قانون الاختبار', 'Texte de l''article cinq', '2001-01-01'),
  (200, 1, NULL, '1', NULL, 'تعدل المادة الخامسة من قانون الاختبار كما يلي', NULL, '2005-06-01'),
  (400, 1, 41, '6', NULL, 'نص المادة السادسة القديم', 'Ancien texte de l''article six', '2001-01-01'),
  (400, 2, 42, '7', NULL, 'نص المادة السابعة', NULL, '2001-01-01'),
  (400, 3, 42, '7-1', '( أضيفت بموجب : 3 / 2025 )', 'يبرم العقد خطيا', 'Le contrat est conclu par écrit.', '2025-05-09'),
  (430, 1, NULL, '37', NULL, 'نص المادة 37', NULL, '1943-03-01'),
  (244565, 13, NULL, '11', NULL, $t$يحكم على الخصم المتعسف بغرامة قدرها اربعين الف ليرة على الاقل ومليوني ليرة على الاكثرتقضي بها المحكمة من تلقاء نفسها.$t$, NULL, NULL);

-- Article 6 of code 400 reworded in 2023; article 7 repealed in 2010.
INSERT INTO article_version (law_id, article_number, major, minor, valid_from, state, text_ar, text_source) VALUES
  (400, '6', 1, 0, NULL, 'in_force', 'نص المادة السادسة القديم', 'centre'),
  (400, '6', 2, 0, '2023-12-28', 'in_force', 'نص المادة السادسة الجديد', 'amendment'),
  (400, '7', 2, 0, '2010-03-01', 'repealed', NULL, 'none');

INSERT INTO centre_error (law_id, number, note_en, note_ar) VALUES
  (400, '6', 'The Centre''s text repeats article 5.', 'يكرر نص المادة 5.');

INSERT INTO ruling (id, court, number, year, decided_on, title, president, members, text_ar) VALUES
  (9000, 'محكمة التمييز', '15', 2010, '2010-05-01', 'حكم تمييزي رقم 15', 'الرئيس فلان', ARRAY['قاض اول', 'قاض ثان'], 'نص الحكم يتعلق بقانون الاختبار'),
  (9001, 'محكمة التمييز', '16', 2011, '2011-05-01', 'حكم ممسوح رقم 16', NULL, NULL, NULL),
  (9002, NULL, '17', 2012, '2012-05-01', 'حكم مقروء رقم 17', NULL, NULL, NULL);
INSERT INTO ruling_file (ruling_id, bytes, sha256, fetched_at) VALUES (9000, 1, 'x', now()), (9001, 1, 'x', now());
INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at, low_confidence) VALUES
  (9001, 2, 'صعب', 'test', now(), true), (9001, 1, E'نص واحداثنان', 'test', now(), false),
  (9002, 1, 'نص', 'test', now(), false);

INSERT INTO graph_edge (id, from_key, to_key, relation, source, status, from_type, from_id, from_article, to_law_id, to_article, to_cite, part, quote) VALUES
  (1, 'law/200/art/1', 'law/100/art/5', 'amend', 'model', 'linked', 'legislation', 200, '1', 100, '5', 'lb/law/2001-01-01/10/art/5', NULL, 'تعدل المادة الخامسة من قانون الاختبار كما يلي'),
  (2, 'law/200/art/1', 'law/100/art/5', 'amend', 'parser', 'linked', 'legislation', 200, '1', 100, '5', 'lb/law/2001-01-01/10/art/5', NULL, 'تعدل المادة الخامسة من قانون الاختبار كما يلي'),
  (10, 'law/333455/art/1', 'law/190374/art/3', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '3', NULL, NULL, NULL),
  (11, 'law/333455/art/1', 'law/190374/art/4', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '4', NULL, NULL, NULL),
  (12, 'law/333455/art/1', 'law/190374/art/5', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '5', NULL, NULL, NULL),
  (13, 'law/333455/art/1', 'law/190374/art/6', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '6', NULL, NULL, NULL),
  (14, 'law/333455/art/1', 'law/190374/art/7', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '7', NULL, NULL, NULL),
  (15, 'law/333455/art/1', 'law/190374/art/8', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '8', NULL, NULL, NULL),
  (16, 'law/333455/art/1', 'law/190374/art/9', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '9', NULL, NULL, NULL),
  (17, 'law/333455/art/1', 'law/190374/art/10', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '10', NULL, NULL, NULL),
  (18, 'law/333455/art/1', 'law/190374/art/11', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '11', NULL, NULL, NULL),
  (19, 'law/333455/art/1', 'law/190374/art/12', 'insert', 'model', 'linked', 'legislation', 333455, '1', 190374, '12', NULL, NULL, NULL),
  (20, 'law/333455/art/1', 'law/190374/art/3', 'insert', 'parser', 'linked', 'legislation', 333455, '1', 190374, '3', NULL, NULL, NULL),
  (21, 'law/2000200346/art/1', 'law/430/art/37', 'amend', 'model', 'linked', 'legislation', 2000200346, '1', 430, '37', NULL, NULL, NULL),
  (22, 'law/200', 'law/430', 'replace', 'model', 'linked', 'legislation', 200, NULL, 430, NULL, NULL, NULL, NULL),
  (23, 'law/300/art/1', 'law/430/art/99', 'amend', 'model', 'linked', 'legislation', 300, '1', 430, '99', NULL, NULL, 'نننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننننن'),
  (24, 'ruling/2000200346', 'law/430/art/5', 'cites', 'model', 'linked', 'ruling', 2000200346, NULL, 430, '5', NULL, NULL, NULL),
  -- Law 300's general repeal clause, and its citation of law 100: what
  -- the possibly_affected view reads.
  (25, 'law/300', NULL, 'general_repeal', 'parser', 'general', 'legislation', 300, NULL, NULL, NULL, NULL, NULL, NULL),
  (26, 'law/300', 'law/100', 'cites', 'model', 'linked', 'legislation', 300, NULL, 100, NULL, NULL, NULL, NULL);

-- When pages last changed: law 100 and ruling 9000 on 30 Oct 2026; Gazette
-- issue 500 carries the text's own date an old build seeded, which is never
-- given as updated_at.
INSERT INTO page_state (kind, id, content_hash, changed_at) VALUES
  ('law', 100, 'x', '2026-10-30'), ('ruling', 9000, 'x', '2026-10-30'), ('gazette', 500, 'x', '2001-01-05');
