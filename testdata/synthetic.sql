-- Fictional records for a local demo. These are not Lebanese law.
INSERT INTO legislation_kind (name_ar,name_en,name_fr) VALUES ('قانون','Law','Loi');
INSERT INTO legislation (id,kind,number,number_int,issued_on,title,status,preamble)
VALUES (1,'قانون','1',1,'2024-01-01','نص تجريبي عن المكتبة العامة','in_force','هذا نص خيالي لا يمثل أي قانون لبناني.');
INSERT INTO legislation_title_en (law_id,title_en,model)
VALUES (1,'Synthetic public library example','synthetic fixture');
INSERT INTO legislation_article (law_id,position,number,number_int,text_ar,text_fr,in_force_from)
VALUES (1,1,'1',1,'هذا نص تجريبي خيالي عن المكتبة العامة. لا يمثل قانوناً لبنانياً.','Exemple fictif de bibliothèque publique. Ce texte ne constitue pas une loi.','2024-01-01');
INSERT INTO code (slug,law_id,name_en,name_fr,position)
VALUES ('synthetic-library',1,'Synthetic library example','Exemple fictif',1);
INSERT INTO gazette_issue (id,number,published_on) VALUES (1,'DEMO','2024-01-01');
INSERT INTO gazette_entry (issue_id,law_id,pages) VALUES (1,1,'1');
INSERT INTO page_state (kind,id,content_hash,changed_at)
VALUES ('law',1,'synthetic','2024-01-01'),('gazette',1,'synthetic','2024-01-01');
