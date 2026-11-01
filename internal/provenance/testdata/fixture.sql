--
-- PostgreSQL database dump
--


-- Dumped from database version 16.15
-- Dumped by pg_dump version 16.15

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Data for Name: legislation_kind; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('أمر', 49440, 'Order', 'Ordre');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('اتفاقية', 49441, 'Agreement', 'Convention');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('اتفاقية دولية', 49442, 'International agreement', 'Convention internationale');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('اعلام', 49443, 'Notification', 'Notification');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('اعلان', 49444, 'Announcement', 'Annonce');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('افادة', 49445, 'Attestation', 'Attestation');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('الدستور', 49446, 'Constitution', 'Constitution');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('انذار', 49447, 'Formal notice', 'Mise en demeure');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('براءة سلطانية', 49448, 'Sultanic berat', 'Bérat impérial');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('بلاغ', 49449, 'Communiqué', 'Communiqué');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('بيان', 49450, 'Statement', 'Déclaration');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('تعليمات', 49451, 'Instructions', 'Instructions');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('تعميم', 49452, 'Circular', 'Circulaire');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('تقرير', 49453, 'Report', 'Rapport');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('خط همايوني', 49454, 'Imperial rescript (Hatt-ı Hümayun)', 'Hatt-i Humayoun');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('دستور', 49455, 'Constitution', 'Constitution');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('دعوة', 49456, 'Convocation', 'Convocation');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('دفتر شروط', 49457, 'Specifications', 'Cahier des charges');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('رأي', 49458, 'Opinion', 'Avis');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('عقد', 49459, 'Contract', 'Contrat');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('عقد جماعي', 49460, 'Collective agreement', 'Convention collective');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('علم وخبر', 49461, 'Notice of association', 'Récépissé de déclaration');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قانون', 49462, 'Law', 'Loi');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قانون عثماني', 49463, 'Ottoman law', 'Loi ottomane');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قانون منفذ بمرسوم', 49464, 'Law enacted by decree', 'Loi mise en vigueur par décret');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قانون نافذ حكما', 102464, 'Law in force by operation of law', 'Loi exécutoire de plein droit');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار', 49465, 'Decision', 'Arrêté');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار اداري', 49466, 'Administrative decision', 'Arrêté administratif');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار اساسي', 49467, 'Basic decision', 'Arrêté de base');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار المفوض السامي', 49468, 'High Commissioner''s decision', 'Arrêté du Haut-Commissaire');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار بلدي', 49469, 'Municipal decision', 'Arrêté municipal');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار مبدئي', 49470, 'Preliminary decision', 'Décision de principe');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار نافذ حكما', 49471, 'Decision in force by operation of law', 'Arrêté exécutoire de plein droit');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('قرار وسيط', 49472, 'Interim decision', 'Décision intermédiaire');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('كتاب', 49473, 'Letter', 'Lettre');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('لائحة', 49474, 'List', 'Liste');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('محضر', 49475, 'Minutes', 'Procès-verbal');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('محضر جلسة', 49476, 'Minutes of session', 'Procès-verbal de séance');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مختلف', 49477, 'Miscellaneous', 'Divers');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مذكرة', 49478, 'Memorandum', 'Note');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مذكرة خدمة', 49479, 'Service memorandum', 'Note de service');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مرسوم', 49480, 'Decree', 'Décret');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مرسوم إشتراعى', 49481, 'Legislative decree', 'Décret-loi');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مرسوم منفذ بقانون', 49482, 'Decree enacted by law', 'Décret mis en vigueur par la loi');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('مرسوم نافذ حكما', 49483, 'Decree in force by operation of law', 'Décret exécutoire de plein droit');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('ملحق', 49484, 'Annex', 'Annexe');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('نظام', 49485, 'Regulation', 'Règlement');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('نظام داخلي', 49486, 'Internal regulation', 'Règlement intérieur');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('نموذج', 49487, 'Form', 'Formulaire');
INSERT INTO public.legislation_kind (name_ar, centre_id, name_en, name_fr) VALUES ('غير محدد', NULL, 'Unspecified', 'Non précisé');


--
-- Data for Name: legislation; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation (id, kind, number, number_int, issued_on, title, status, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES (8225, 'قانون', '183', 183, '2000-05-24', 'الاجازة للحكومة الانضمام الى اتفاقيات العمل العربية رقم 1 - 9 - 13 - 15 - 17 - 18', 'in_force', '25', '2000-06-08', '1847', 'اقر مجلس النواب،

وينشر رئيس الجمهورية القانون التالي نصه:', 'اقترحت وزارة العمل التصديق على اتفاقيات العمل العربية التالية أسماؤها وفقاً لأرقامها:

رقم 1: بشأن مستويات العمل (1966)

رقم 9: بشأن التوجيه والتدريب المهني (1977)

رقم 13: بشأن بينة العمل (1981)

رقم 15: بشأن تحديد وحماية الأجور (1983)

رقم 17: بشأن تأهيل وتشغيل المعوقين (1993)

رقم 18: بشأن عمل الأحداث (1996)

تندرج هذه الاتفاقيات في إطار تطوير أحكام التشريع اللبناني في المواضيع التي تعالجها هذه الاتفاقيات، وصولاً إلى توحيد التشريعات العمالية وظروف العمل في الدول العربية.

وتجدر الإشارة أنه بالنسبة إلى اتفاقية العمل العربية (رقم 1) المتعلقة بمستويات العمل، فقد رأت وزارة العمل أن أحكام الجزئين الأول والثالث لا تتعارض مع أحكام القوانين المرعية الإجراء، أو أن قانون العمل (1946) لم يأت على تناولها كما في موضوع الهجرة للعمل وإن هذا الأمر لا ترى فيه إعاقة أمام التصديق. أما المواد المختارة من الجزء الثاني من الاتفاقية وتبلغ 52 مادة فتتفق في مضمونها مع أحكام التشريع النافذ أو أنها لا تتعارض بشكل جوهري مع هذا التشريع أو لا يوجد نص قانوني يمنع ما ورد فيها.

أما الاتفاقيات رقم 9 و 13 و 15 المذكورة أعلاه، فهي معروضة للتصديق بكافة نصوصها ذلك أن أحكامها لا تفسح مجالاً لاختيار مواد محددة منها للالتزام فقط بها.

في حين أنه بالنسبة إلى الاتفاقيتين رقم 17 و18، فإن المعروض للتصديق هو المواد الإلزامية بطبيعة الحال والمواد المختارة من كل منهما من بين الأحكام التي تجيز الاتفاقية ذاتها الاختيار من بينها، مع الإشارة إلى أنه بالإمكان الارتباط في أي وقت بأية مادة واردة في المواد الاختيارية التي لم تكن قد اختيرت سابقاً وفقاً للأصول المتبعة.

وبما أن عملية التصديق تدخل في إطار الالتزام بدستور وأهداف منظمة العمل العربية، وهي أيضاً خطوة تصب في تطوير تشريع العمل اللبناني قدر الإمكان.

وعلماً أن لبنان لم يبرم بعد أياً من اتفاقيات العمل العربية التي أقرتها المنظمة حتى العام 1996 بسبب ظروفه الصعبة.

وبما أن وزارة العمل طلبت التصديق على الاتفاقيات المذكورة أعلاه.

وبما أن الانضمام إلى هذه الاتفاقيات يتطلب استصدار قانون بإجازة الانضمام عملاً بالمادة 52 من الدستور، لذلك تتقدم الحكومة من المجلس النيابي الكريم بمشروع القانون المرفق الذي يجيز لها الانضمام إليها راجية إقراره.', 'بعبدا في 24 ايار 2000
الامضاء: اميل لحود
صدر عن رئيس الجمهورية
رئيس مجلس الوزراء
الامضاء: سليم الحص
رئيس مجلس الوزراء
الامضاء: سليم الحص');
INSERT INTO public.legislation (id, kind, number, number_int, issued_on, title, status, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES (9307, 'قانون', '49', 49, '2008-12-13', 'تعديل المادة الأولى من القانون رقم 183 تاريخ 24/5/2000 المتعلق بالإجازة للحكومة الانضمام إلى اتفاقيات العمل العربية رقم 1 ـ 9 ـ 13 ـ 15 ـ 17 ـ 18', 'in_force', '56', '2008-12-18', '5591-5592', 'أقر مجلس النواب،

وينشر رئيس الجمهورية القانون التالي نصه:', 'كانت وزارة العمل قد أحالت الى وزارة الخارجية بموجب كتابها رقم 876/3 تاريخ 26/4/1996 اتفاقية العمل العربية رقم (1) بشأن مستويات العمل مقترحة احالة مشروع قانون الى مجلس الوزراء باجازة التصديق على هذه الاتفاقية مع تعداد المواد المقترح التصديق عليها (المواد الالزامية والمواد المختارة منها).

وبتاريخ 20/5/97، وبموجب كتابنا رقم 1204/3 احالت وزارة العمل الى وزارة الخارجية اتفاقيات العمل العربية رقم: 9 - 13 - 15 - 17 و18 مقترحة ايضا احالة مشروع قانون الى مجلس الوزراء باجازة التصديق على هذه الاتفاقيات مع لفت النظر الى ما يأتي:

* التصديق على كافة مواد الاتفاقيات رقم: 9 - 13 و15 .

* التصديق على المواد المقترحة مع تعداد ارقامها بالنسبة الى الاتفاقيتين رقم 17 و18 (المواد الالزامية والمواد المختارة منهما).

وقد ارفقت هذه الاتفاقيات بجداول مقارنة تبين أحكام المواد المقترح التصديق عليها والوضع التشريعي في لبنان بالنسبة الى نصوصها مع الاسباب الموجبة لكل اتفاقية.

وبموجب المرسوم رقم 2509 تاريخ 21/2/2000 احيل مشروع قانون الى مجلس النواب يرمي الى الاجازة للحكومة الانضمام الى اتفاقيات العمل العربية رقم: 1 - 9 - 13 - 15 - 17 و18 .

وبعد ان تبين لوزارة العمل ان المرسوم رقم 2509 احال هذه الاتفاقيات الى مجلس النواب للاجازة للحكومة الانضمام اليها دون الاخذ بعين الاعتبار وجهة نظرها بالنسبة الى المواد المقترح التصديق عليها، فوجهت الى رئاسة مجلس الوزراء الكتاب رقم 610/3 تاريخ 28/3/2000 (المرفق ربطا) تؤكد فيه وجهة نظرها بأن التصديق على الاتفاقيات رقم (1) و(17) و(18) يجب أن يقتصر على المواد التي عددتها في كتابها دون غيرها، علما ان هذه الاتفاقيات تسمح للدولة التي ترغب في التصديق عليها أن تختار من موادها غير الالزامية عددا محددا من المواد الاختيارية التي تتلاءم مع اوضاعها الاجتماعية والاقتصادية وترى الدولة انها لا تشكل تناقضا صريحا لنصوصها القانونية المرعية الاجراء.

وحيث ان القانون رقم 183 تاريخ 24/5/2000 قد صدر بالاجازة للحكومة الانضمام الى اتفاقيات العمل العربية رقم: 1 - 9 - 13 - 15 - 17 و18 بكافة أحكامها دون أي استثناء بالنسبة للاتفاقيات رقم: 1 - 17 و18 مما جعل هذه الوزارة تضع مشروع تعديل للقانون رقم 183 تاريخ 24/5/2000 يتضمن الاستثناءات المطلوبة.

وبما أن التعديل المطلوب يستلزم إعداد مشروع قانون يجيز هذا التعديل.

لذلك، تتقدم الحكومة من المجلس النيابي الكريم بمشروع القانون المرفق راجية إقراره.', 'بعبدا في 13 كانون الأول 2008
الإمضاء: ميشال سليمان
صدر عن رئيس الجمهورية
رئيس مجلس الوزراء
الإمضاء: فؤاد السنيورة
رئيس مجلس الوزراء
الإمضاء: فؤاد السنيورة');
INSERT INTO public.legislation (id, kind, number, number_int, issued_on, title, status, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES (326577, 'قرار', '1451', 1451, '2022-12-05', 'تعديل القرار رقم 1125 تاريخ 11/10/2022 الـمتعلق بحل مجلس بلدية مشتى حمود ـ محافظة عكار', 'in_force', '54', '2022-12-15', '3443', 'ان وزير الداخلية والبلديات،

بناء على الـمرسوم رقم 8376 تاريخ 10/9/2021، (تشكيل الحكومة(،

بناء على القرار رقم 1125 تاريخ 11/10/2022 الصادر عن وزير الداخلية والبلديات، والـمتعلق بحل مجلس بلدية مشتى حمود، وتكليف محافظ عكار القيام بأعماله، وذلك حتى انتخاب مجلس بلدي جديد،

بناء على كتاب محافظ عكار 733 تاريخ 8/11/2022، الـمتضمن طلب اعفائه من القيام بأعمال بعض الـمجالس البلدية الـمنحلة واقتراحه بتكليف أمين سر عام الـمحافظة للقيام بأعمال الـمجالس البلدية الـمنحلة، لا سيما بلدية مشتى حمود،

بناء على نص الـمادة 24 من الـمرسوم الإشتراعي رقم 118 تاريخ 30/6/1977 )قانون البلديات) وتعديلاته،

بناء على اقتراح مدير عام الشؤون السياسية واللاجئين,

يقرر ما يأتي:', NULL, 'بيروت في 5 كانون الأول 2022
وزير الداخلية والبلديات
بسام مولوي');
INSERT INTO public.legislation (id, kind, number, number_int, issued_on, title, status, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES (327389, 'قرار', '882', 882, '2023-07-06', 'تعديل القرار رقم 1451 تاريخ 5/12/2022 الـمتعلق بحل مجلس بلدية مشتى حمود ـ محافظة عكار', 'in_force', '29', '2023-07-13', '2604', 'ان وزير الداخلية والبلديات،

بناء على الـمرسوم رقم 8376 تاريخ 10/9/2021، (تشكيل الحكومة(،

بناء على القرار رقم 1451 تاريخ 5/12/2022 الصادر عن وزير الداخلية والبلديات، والـمتعلق بتكليف امين سر عام محافظة عكار القيام بمهام مجلس بلدية مشتى حمود،

بناء على احالة محافظ عكار رقم 461/ب تـاريـخ 21/6/2023،

بناء على نص الـمادة 24 من الـمرسوم الإشتراعي رقم 118 تاريخ 30/6/1977 (قانون البلديات) وتعديلاته،

بناء على اقتراح مدير عام الشؤون السياسية واللاجئين,

يقرر ما يأتي:', NULL, 'بيروت في 6 تموز 2023
وزير الداخلية والبلديات
بسام مولوي');
INSERT INTO public.legislation (id, kind, number, number_int, issued_on, title, status, gazette_number, gazette_date, gazette_pages, preamble, explanatory_note, closing) VALUES (2000197135, 'قرار', '732', 732, '2025-08-19', 'يتعلق بتوجب رسم الطابع المالي النسبي على بعض العقود والملاحق التعديلية', 'in_force', '37', '2025-08-28', '2592', 'إن وزير المالية،

بناء على المرسوم رقم ٥٣ تاريخ ٢٠٢٥/٢/٨ (تشكيل الحكومة)،

بناء على المرسوم الإشتراعي رقم ٦٧ تاريخ ١٩٦٧/٨/٥ وتعديلاته (قانون رسم الطابع المالي) لاسيما المادتين ١٣ و١٦ منه،

بناء على اقتراح مدير المالية العام،

وبعد إستشارة مجلس شورى الدولة (رأي رقم ٢٠٢٤/٢٠٣-٢٠٢٥ تاريخ ٢٠٢٥/٦/٣)،

يقرر ما يأتي:', NULL, NULL);


--
-- Data for Name: agreement; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: amendment; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.amendment (id, by_law_id, by_article, seq, action, law_id, article_number, part, text_ar, old_text_ar, effective_on, status, reason, method, resolved_by, judged_by, judgement, evidence) VALUES (1, 9307, '1', 1, 'replace', 8225, '1', NULL, '1 ـ الاتفاقية رقم (1) بشأن مستويات العمل، 1966:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ أحكام الجزء الأول.

ـ ثلاثة وخمسين مادة (53) من أحكام الجزء الثاني وهي المواد: 16 ـ 18 ـ 20 ـ 21 ـ 22 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32 ـ 33 ـ 36 ـ 37 ـ 39 ـ 40 ـ 41 ـ 42 ـ 46 ـ 47 ـ 50 ـ 51 ـ 52 ـ 53 ـ 54 ـ 55 ـ 56 ـ 60 ـ 63 ـ 66 ـ 68 ـ 69 ـ76- 81 ـ 84 ـ 85 ـ 86ـ 87 ـ 88ـ 89 ـ 90 ـ 92 ـ 93 ـ 94 ـ 95 ـ 96 ـ 97 ـ 98 ـ 99 و100.

ـ وأحكام الجزء الثالث.

2 ـ الاتفاقية رقم (9) بشأن التوجيه والتدريب المهني ـ 1997:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

3 ـ الاتفاقية رقم (13) بشأن بيئة العمل ـ 1981:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

4 ـ الاتفاقية رقم (15) بشأن تحديد وحماية الأجور ـ 1983:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

5 ـ الاتفاقية رقم (17) بشأن تأهيل وتشغيل المعوقين ـ 1993:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ الأحكام الواردة تحت البند «أولا» (التعريف والتصنيف ونطاق التطبيق).

ـ خمسة عشر مادة هي التالية: 6 ـ 8 ـ 9 ـ 10 ـ 11 ـ 13 ـ 15 ـ 16 ـ 17 ـ 18 ـ 19 ـ 20 ـ 22 ـ 23 ـ 26

ـ الأحكام الواردة تحت البند «ثامنا» (الأحكام العامة).

6 ـ الاتفاقية رقم (18) بشأن عمل الأحداث ـ 1996:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

الأحكام الواردة في البند «أولا» (نطاق التطبيق).

ـ ثلاثة وعشرين مادة هي التالية: 3 ـ 4 ـ 5 ـ 6 ـ 9 ـ 11 ـ 12 ـ 13 ـ 14 ـ 15 ـ 19 ـ 20 ـ 22 ـ 23 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32

ـ الأحكام الواردة في البند «سادسا» (الأحكام العامة).', NULL, '2008-12-18', 'applied', 'not in the Centre''s text', 'pattern', 'number and date', 'gemini-3.1-pro-preview', 'predates', 'عدلت الماده الاولي من القانون رقم 183 تاريخ 24/5/2000 المتعلقه بالاجازه للحكومه الانضمام الي اتفاقيات العمل العربيه, وذلك علي ');
INSERT INTO public.amendment (id, by_law_id, by_article, seq, action, law_id, article_number, part, text_ar, old_text_ar, effective_on, status, reason, method, resolved_by, judged_by, judgement, evidence) VALUES (2, 326577, '1', 1, 'replace', NULL, '2', NULL, 'ـ يتولى امين السر العام في محافظة عكار القيام بمهام مجلس بلدية مشتى حمود', 'محافظ عكار حتى انتخاب مجلس بلدي جديد.

والباقي دون تعديل.', '2022-12-15', 'review', 'no text with that number', 'pattern', NULL, NULL, NULL, 'تعدل الماده الثانيه من القرار رقم 1125 تاريخ 11/10/2022 المتعلق بحل مجلس بلديه مشتي حمود محافظه عكار, لتصبح علي الشكل التالي: يت');
INSERT INTO public.amendment (id, by_law_id, by_article, seq, action, law_id, article_number, part, text_ar, old_text_ar, effective_on, status, reason, method, resolved_by, judged_by, judgement, evidence) VALUES (3, 327389, '1', 1, 'replace', 326577, '1', NULL, 'ــ يتولى محافظ عكار القيام بمهام مجلس بلدية مشتى حمود', 'امين سر عام محافظة عكار حتى انتخاب مجلس بلدي فيها.

والباقي دون تعديل.', '2023-07-13', 'applied', NULL, 'pattern', 'number and date', NULL, NULL, 'تعدل الماده الاولي من القرار رقم 1451 تاريخ 5/12/2022 المتعلقه بحل مجلس بلديه مشتي حمود محافظه عكار, ليصبح علي الشكل التالي: يتو');


--
-- Data for Name: article_version; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.article_version (law_id, article_number, major, minor, valid_from, valid_to, state, text_ar, text_source, amendment_id, note, source_kind, source_id) VALUES (8225, '1', 1, 0, NULL, '2008-12-18', 'in_force', 'مادة 1: اجيز للحكومة الانضمام الى اتفاقيات العمل العربية التالية التي اعتمدها مؤتمر العمل العربي. 1­ الاتفاقية رقم (1) بشأن مستويات العمل, 1966 2­ الاتفاقية رقم (9) بشأن التوجيه والتدريب الصهني, الاسكندرية­ اذار 1977 3­ الاتفاقية رقم (13) بشأن بيئة العمل, بنغازي­ اذار 1981 4­ الاتفاقية رقم (15) بشأن تحديد وحماية الاجور, عمان ­ اذار 1983 5­ الاتفاقية رقم (17) بشأن تأهيل وتشغيل المعوقين, عمان­ نيسان 6­ الاتفاقية رقم (18) بشأن عمل الاحداث, القاهرة ­ اذار 1996, المرفقة ربطا.', 'centre', NULL, NULL, 'ul', 'lawtext/8225');
INSERT INTO public.article_version (law_id, article_number, major, minor, valid_from, valid_to, state, text_ar, text_source, amendment_id, note, source_kind, source_id) VALUES (8225, '1', 2, 0, '2008-12-18', NULL, 'in_force', '1 ـ الاتفاقية رقم (1) بشأن مستويات العمل، 1966:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ أحكام الجزء الأول.

ـ ثلاثة وخمسين مادة (53) من أحكام الجزء الثاني وهي المواد: 16 ـ 18 ـ 20 ـ 21 ـ 22 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32 ـ 33 ـ 36 ـ 37 ـ 39 ـ 40 ـ 41 ـ 42 ـ 46 ـ 47 ـ 50 ـ 51 ـ 52 ـ 53 ـ 54 ـ 55 ـ 56 ـ 60 ـ 63 ـ 66 ـ 68 ـ 69 ـ76- 81 ـ 84 ـ 85 ـ 86ـ 87 ـ 88ـ 89 ـ 90 ـ 92 ـ 93 ـ 94 ـ 95 ـ 96 ـ 97 ـ 98 ـ 99 و100.

ـ وأحكام الجزء الثالث.

2 ـ الاتفاقية رقم (9) بشأن التوجيه والتدريب المهني ـ 1997:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

3 ـ الاتفاقية رقم (13) بشأن بيئة العمل ـ 1981:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

4 ـ الاتفاقية رقم (15) بشأن تحديد وحماية الأجور ـ 1983:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

5 ـ الاتفاقية رقم (17) بشأن تأهيل وتشغيل المعوقين ـ 1993:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ الأحكام الواردة تحت البند «أولا» (التعريف والتصنيف ونطاق التطبيق).

ـ خمسة عشر مادة هي التالية: 6 ـ 8 ـ 9 ـ 10 ـ 11 ـ 13 ـ 15 ـ 16 ـ 17 ـ 18 ـ 19 ـ 20 ـ 22 ـ 23 ـ 26

ـ الأحكام الواردة تحت البند «ثامنا» (الأحكام العامة).

6 ـ الاتفاقية رقم (18) بشأن عمل الأحداث ـ 1996:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

الأحكام الواردة في البند «أولا» (نطاق التطبيق).

ـ ثلاثة وعشرين مادة هي التالية: 3 ـ 4 ـ 5 ـ 6 ـ 9 ـ 11 ـ 12 ـ 13 ـ 14 ـ 15 ـ 19 ـ 20 ـ 22 ـ 23 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32

ـ الأحكام الواردة في البند «سادسا» (الأحكام العامة).', 'amendment', 1, NULL, 'amendment', '9307/1');
INSERT INTO public.article_version (law_id, article_number, major, minor, valid_from, valid_to, state, text_ar, text_source, amendment_id, note, source_kind, source_id) VALUES (326577, '1', 1, 0, NULL, '2023-07-13', 'in_force', 'امين سر عام محافظة عكار حتى انتخاب مجلس بلدي فيها.

والباقي دون تعديل.', 'amendment', NULL, NULL, 'amendment', '327389/1');
INSERT INTO public.article_version (law_id, article_number, major, minor, valid_from, valid_to, state, text_ar, text_source, amendment_id, note, source_kind, source_id) VALUES (326577, '1', 2, 0, '2023-07-13', NULL, 'in_force', 'ــ يتولى محافظ عكار القيام بمهام مجلس بلدية مشتى حمود', 'amendment', 3, NULL, 'amendment', '327389/1');


--
-- Data for Name: centre_error; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: code; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: court; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('إستئناف مدني', 49973, 'Civil Court of Appeal', 'Cour d''appel civile');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('ابتدائي مدني', 113983, 'Civil Court of First Instance', 'Tribunal civil de première instance');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('استئناف جزائي', 49974, 'Criminal Court of Appeal', 'Cour d''appel pénale');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('المجلس الدستوري', 114173, 'Constitutional Council', 'Conseil constitutionnel');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('المجلس العدلي', 102412, 'Judicial Council', 'Conseil de justice');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('تمييز جزائي - مطبوعات', 101411, 'Court of Cassation, criminal chamber (press)', 'Cour de cassation, chambre pénale (presse)');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('تمييز جزائي', 49975, 'Court of Cassation, criminal chamber', 'Cour de cassation, chambre pénale');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('تمييز مدني', 49976, 'Court of Cassation, civil chamber', 'Cour de cassation, chambre civile');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('شورى', 49977, 'State Council', 'Conseil d''État');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('مجالس العمل التحكيمية', 49978, 'Labour Arbitration Councils', 'Conseils arbitraux du travail');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('محكمة الاستئناف الناظرة بجرائم المطبوعات', 114064, 'Court of Appeal for press offences', 'Cour d''appel des délits de presse');
INSERT INTO public.court (name_ar, centre_id, name_en, name_fr) VALUES ('محكمة الجنايات', 49979, 'Criminal Court', 'Cour criminelle');


--
-- Data for Name: date_correction; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: doctrine_article; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: gazette_issue; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.gazette_issue (id, number, published_on) VALUES (2002025037, '37', '2025-08-28');


--
-- Data for Name: gazette_entry; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.gazette_entry (issue_id, law_id, pages) VALUES (2002025037, 2000197135, '2592');


--
-- Data for Name: graph_edge; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.graph_edge (id, from_key, to_key, relation, source, status, from_type, from_id, from_article, to_law_id, to_article, to_cite, part, how, quote, model) VALUES (1, 'gazette/2002025037', 'law/2000197135', 'publishes', 'centre', 'linked', 'gazette', 2002025037, NULL, 2000197135, NULL, NULL, NULL, 'the gazette''s contents', NULL, NULL);
INSERT INTO public.graph_edge (id, from_key, to_key, relation, source, status, from_type, from_id, from_article, to_law_id, to_article, to_cite, part, how, quote, model) VALUES (2, 'law/9307/art/1', 'law/8225/art/1', 'replace', 'parser', 'linked', 'legislation', 9307, '1', 8225, '1', NULL, NULL, 'number and date', 'عدلت الماده الاولي من القانون رقم 183 تاريخ 24/5/2000 المتعلقه بالاجازه للحكومه الانضمام الي اتفاقيات العمل العربيه, وذلك علي ', NULL);
INSERT INTO public.graph_edge (id, from_key, to_key, relation, source, status, from_type, from_id, from_article, to_law_id, to_article, to_cite, part, how, quote, model) VALUES (3, 'law/327389/art/1', 'law/326577/art/1', 'replace', 'parser', 'linked', 'legislation', 327389, '1', 326577, '1', NULL, NULL, 'number and date', 'تعدل الماده الاولي من القرار رقم 1451 تاريخ 5/12/2022 المتعلقه بحل مجلس بلديه مشتي حمود محافظه عكار, ليصبح علي الشكل التالي: يتو', NULL);


--
-- Data for Name: legislation_alias; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: legislation_section; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation_section (id, law_id, parent_id, "position", depth, heading, note) VALUES (8459, 8225, NULL, 1, 0, 'المواد', NULL);
INSERT INTO public.legislation_section (id, law_id, parent_id, "position", depth, heading, note) VALUES (9541, 9307, NULL, 1, 0, 'المواد', NULL);
INSERT INTO public.legislation_section (id, law_id, parent_id, "position", depth, heading, note) VALUES (338107, 326577, NULL, 1, 0, 'المواد', NULL);
INSERT INTO public.legislation_section (id, law_id, parent_id, "position", depth, heading, note) VALUES (338883, 327389, NULL, 1, 0, 'المواد', NULL);


--
-- Data for Name: legislation_article; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (8225, 1, NULL, 8459, '1', 1, NULL, 'مادة 1: اجيز للحكومة الانضمام الى اتفاقيات العمل العربية التالية التي اعتمدها مؤتمر العمل العربي. 1­ الاتفاقية رقم (1) بشأن مستويات العمل, 1966 2­ الاتفاقية رقم (9) بشأن التوجيه والتدريب الصهني, الاسكندرية­ اذار 1977 3­ الاتفاقية رقم (13) بشأن بيئة العمل, بنغازي­ اذار 1981 4­ الاتفاقية رقم (15) بشأن تحديد وحماية الاجور, عمان ­ اذار 1983 5­ الاتفاقية رقم (17) بشأن تأهيل وتشغيل المعوقين, عمان­ نيسان 6­ الاتفاقية رقم (18) بشأن عمل الاحداث, القاهرة ­ اذار 1996, المرفقة ربطا.', NULL, NULL, NULL, 'ul', 'lawtext/8225');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (8225, 2, NULL, 8459, '2', 2, NULL, 'يعمل بهذا القانون فور نشره في الجريدة الرسمية.', NULL, NULL, NULL, 'ul', 'lawtext/8225');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (9307, 1, NULL, 9541, '1', 1, NULL, 'عدلت المادة الأولى من القانون رقم 183 تاريخ 24/5/2000 المتعلقة بالإجازة للحكومة الانضمام إلى اتفاقيات العمل العربية، وذلك على النحو التالي:

1 ـ الاتفاقية رقم (1) بشأن مستويات العمل، 1966:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ أحكام الجزء الأول.

ـ ثلاثة وخمسين مادة (53) من أحكام الجزء الثاني وهي المواد: 16 ـ 18 ـ 20 ـ 21 ـ 22 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32 ـ 33 ـ 36 ـ 37 ـ 39 ـ 40 ـ 41 ـ 42 ـ 46 ـ 47 ـ 50 ـ 51 ـ 52 ـ 53 ـ 54 ـ 55 ـ 56 ـ 60 ـ 63 ـ 66 ـ 68 ـ 69 ـ76- 81 ـ 84 ـ 85 ـ 86ـ 87 ـ 88ـ 89 ـ 90 ـ 92 ـ 93 ـ 94 ـ 95 ـ 96 ـ 97 ـ 98 ـ 99 و100.

ـ وأحكام الجزء الثالث.

2 ـ الاتفاقية رقم (9) بشأن التوجيه والتدريب المهني ـ 1997:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

3 ـ الاتفاقية رقم (13) بشأن بيئة العمل ـ 1981:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

4 ـ الاتفاقية رقم (15) بشأن تحديد وحماية الأجور ـ 1983:

إن الموافقة تشمل كافة أحكام هذه الاتفاقية.

5 ـ الاتفاقية رقم (17) بشأن تأهيل وتشغيل المعوقين ـ 1993:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

ـ الأحكام الواردة تحت البند «أولا» (التعريف والتصنيف ونطاق التطبيق).

ـ خمسة عشر مادة هي التالية: 6 ـ 8 ـ 9 ـ 10 ـ 11 ـ 13 ـ 15 ـ 16 ـ 17 ـ 18 ـ 19 ـ 20 ـ 22 ـ 23 ـ 26

ـ الأحكام الواردة تحت البند «ثامنا» (الأحكام العامة).

6 ـ الاتفاقية رقم (18) بشأن عمل الأحداث ـ 1996:

إن الموافقة على هذه الاتفاقية تشمل:

ـ الديباجة.

الأحكام الواردة في البند «أولا» (نطاق التطبيق).

ـ ثلاثة وعشرين مادة هي التالية: 3 ـ 4 ـ 5 ـ 6 ـ 9 ـ 11 ـ 12 ـ 13 ـ 14 ـ 15 ـ 19 ـ 20 ـ 22 ـ 23 ـ 24 ـ 25 ـ 26 ـ 27 ـ 28 ـ 29 ـ 30 ـ 31 ـ 32

ـ الأحكام الواردة في البند «سادسا» (الأحكام العامة).', NULL, NULL, NULL, 'ul', 'lawtext/9307');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (9307, 2, NULL, 9541, '2', 2, NULL, 'يعمل بهذا القانون فور نشره في الجريدة الرسمية.', NULL, NULL, NULL, 'ul', 'lawtext/9307');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (326577, 1, NULL, 338107, '1', 1, NULL, 'تعدل الـمادة الثانية من القرار رقم 1125 تاريخ 11/10/2022 الـمتعلق بحل مجلس بلدية مشتى حمود ــ محافظة عكار، لتصبح على الشكل التالي:

ـ يتولى امين السر العام في محافظة عكار القيام بمهام مجلس بلدية مشتى حمود بدلا من محافظ عكار حتى انتخاب مجلس بلدي جديد.

والباقي دون تعديل.', NULL, NULL, NULL, 'ul', 'lawtext/326577');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (326577, 2, NULL, 338107, '2', 2, NULL, 'ينشر هذا القرار ويبلغ حيث تدعو الحاجة.', NULL, NULL, NULL, 'ul', 'lawtext/326577');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (327389, 1, NULL, 338883, '1', 1, NULL, 'تعدل الـمادة الأولى من القرار رقم 1451 تاريخ 5/12/2022 الـمتعلقة بحل مجلس بلدية مشتى حمود ــ محافظة عكار، ليصبح على الشكل التالي:

ــ يتولى محافظ عكار القيام بمهام مجلس بلدية مشتى حمود بدلا من امين سر عام محافظة عكار حتى انتخاب مجلس بلدي فيها.

والباقي دون تعديل.', NULL, NULL, NULL, 'ul', 'lawtext/327389');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (327389, 2, NULL, 338883, '2', 2, NULL, 'ينشر هذا القرار وييلغ حيث تدعو الحاجة.', NULL, NULL, NULL, 'ul', 'lawtext/327389');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 1, NULL, NULL, '1', 1, NULL, 'تتخذ أساسا لفرض رسم الطابع المالي النسبي المبالغ التي تتناولها بشكل صريح أو ضمني، العقود والإتفاقيات الخاضعة.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 2, NULL, NULL, '2', 2, NULL, 'تخضع الملاحق التعديلية التي تتناول زيادة قيمة العقود أو الإتفاقيات الأساسية لرسم الطابع المالي النسبي على أساس قيمة الزيادة فقط دون المبلغ الأساسي الذي سبق تسديد الرسم عنه، وفي حال تعديل قيمة الدفعات المستحقة نتيجة الزيادة الحاصلة على القيمة الإجمالية فيبقى الرسم متوجبا على الزيادة فقط.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 3, NULL, NULL, '3', 3, NULL, 'لا تخضع لرسم الطابع المالي النسبي الملاحق التعديلية التي تتناول تخفيض قيمة العقود أو الإتفاقيات الأساسية التي سبق أن سدد الرسم عنها على أساس المبلغ قبل التخفيض، حتى لو تضمنت هذه الملاحق تعديلاً في قيمة الدفعات المستحقة نتيجة التخفيض الحاصل.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 4, NULL, NULL, '4', 4, NULL, 'تخضع لرسم الطابع المالي النسبي عقود البيع مع شرط حق الإسترداد أو البيع الوفائي على أساس قيمة البيع، إلا في حال كان المبلغ المحدد لممارسة حق الإسترداد أكبر فعندها يحتسب رسم الطابع المالي على أساس المبلغ الأكبر.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 5, NULL, NULL, '5', 5, NULL, 'إذا تضمنت العقود والإتفاقيات أحكاما وبنودا غير تلك المشار إليها في المواد السابقة فيحتسب رسم الطابع المالي النسبي على هذه الأحكام والبنود وفقا للأسس القانونية الخاصة بها.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 6, NULL, NULL, '6', 6, NULL, 'يجب عرض العقود والإتفاقيات على الدائرة المالية المختصة للبت بمدى خضوعها لرسم الطابع المالي النسبي أو إعفائها منه.', NULL, NULL, NULL, 'saderlex', '197135');
INSERT INTO public.legislation_article (law_id, "position", centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from, source_kind, source_id) VALUES (2000197135, 7, NULL, NULL, '7', 7, NULL, 'يعمل بهذا القرار فور صدوره وينشر في الجريدة الرسمية وعلى الموقع الإلكتروني لوزارة المالية.

١٩ آب ٢٠٢٥

وزير المالية

ياسين جابر', NULL, NULL, NULL, 'saderlex', '197135');


--
-- Data for Name: legislation_scan; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation_scan (law_id, source, source_id, source_url, pages, agreement, flagged) VALUES (2000197135, 'saderlex', 197135, 'https://saderlex.com/gazette/pdf/197135', 1, 0.9965157, 1);


--
-- Data for Name: legislation_title_en; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.legislation_title_en (law_id, title_en, model) VALUES (8225, 'Authorizing the Government to accede to Arab Labor Conventions Nos. 1 - 9 - 13 - 15 - 17 - 18', 'sonnet');
INSERT INTO public.legislation_title_en (law_id, title_en, model) VALUES (9307, 'Amending Article 1 of Law No. 183 dated 24/5/2000 on authorizing the Government to accede to Arab Labour Conventions Nos. 1, 9, 13, 15, 17 and 18', 'sonnet');
INSERT INTO public.legislation_title_en (law_id, title_en, model) VALUES (326577, 'Amendment of Decision No. 1125 dated 11/10/2022 concerning the dissolution of the Mishta Hammoud municipal council - Akkar Governorate', 'sonnet');
INSERT INTO public.legislation_title_en (law_id, title_en, model) VALUES (327389, 'Amendment of Decision No. 1451 dated 5/12/2022 concerning the dissolution of the municipal council of Machta Hammoud, Akkar Governorate', 'sonnet');
INSERT INTO public.legislation_title_en (law_id, title_en, model) VALUES (2000197135, 'Concerning the liability for proportional fiscal stamp duty on certain contracts and amending addenda', 'sonnet');


--
-- Data for Name: noindex_page; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.noindex_page (kind, id, "position", reason) VALUES ('article', 8225, 2, 'boilerplate');
INSERT INTO public.noindex_page (kind, id, "position", reason) VALUES ('article', 9307, 2, 'boilerplate');
INSERT INTO public.noindex_page (kind, id, "position", reason) VALUES ('article', 326577, 2, 'boilerplate');


--
-- Data for Name: opinion; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: page_state; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('gazette', 2002025037, 'eff564de0c71e2ca9556fa21b4cad611', '2025-08-28 00:00:00+00');
INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('law', 8225, '79f1c55543b43e140c123d2d7b2b8e9d', '2008-12-18 00:00:00+00');
INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('law', 9307, '43a9defb7f87a61bef47b54c69ccd0c9', '2008-12-18 00:00:00+00');
INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('law', 326577, '8447aaa34854dba8c539248c87ce3c16', '2023-07-13 00:00:00+00');
INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('law', 327389, '61323d49fdfe58a698cbc3e3def9e98c', '2023-07-13 00:00:00+00');
INSERT INTO public.page_state (kind, id, content_hash, changed_at) VALUES ('law', 2000197135, '65c89b8ff9cbf4252e810f76c26e488d', '2025-08-28 00:00:00+00');


--
-- Data for Name: reference; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: research; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: ruling; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: ruling_file; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: ruling_page; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: source_page; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('law', 8225, 'https://legallaw.ul.edu.lb/Law.aspx?lawId=8225&language=ar', 'archive/law/0008/8225.html', '4b91728be9184aea534c99a3fb1f6bfb0bc0686d7a270f4497326ce050283b19', '2026-09-26 14:20:25+00', '2026-09-26 14:20:25+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('lawtext', 8225, 'https://legallaw.ul.edu.lb/LawView.aspx?opt=view&LawID=8225', 'archive/lawtext/0008/8225.html', '7a3b4727ec5b01771e6a4f4e51412798e90314f652917e41b86a1ef07df69c06', '2026-10-01 14:12:45+00', '2026-10-01 14:12:45+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('law', 9307, 'https://legallaw.ul.edu.lb/Law.aspx?lawId=9307&language=ar', 'archive/law/0009/9307.html', '377a4322b7507656b0d14e17a5c6c5796ec3f2daa114d5d6d02df7403474fb6a', '2026-09-26 14:07:05+00', '2026-09-26 14:07:05+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('lawtext', 9307, 'https://legallaw.ul.edu.lb/LawView.aspx?opt=view&LawID=9307', 'archive/lawtext/0009/9307.html', '6a93d30ad406884370c8afa0aa01c3ad1e54ab7b6c5f7aa059813112de2ad7e7', '2026-10-01 14:05:03+00', '2026-10-01 14:05:03+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('law', 326577, 'https://legallaw.ul.edu.lb/Law.aspx?lawId=326577&language=ar', 'archive/law/0326/326577.html', 'e83133382921d10bc80ab02c97c0614e6a1e6c4fbb794650cdee58e54ec009ea', '2026-09-25 17:47:28+00', '2026-09-25 17:47:28+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('lawtext', 326577, 'https://legallaw.ul.edu.lb/LawView.aspx?opt=view&LawID=326577', 'archive/lawtext/0326/326577.html', '3ea9f0401f6f593577f9bbd2679b55cdd5fc4b703a6baf490ea496f147380b45', '2026-09-30 23:49:04+00', '2026-09-30 23:49:04+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('law', 327389, 'https://legallaw.ul.edu.lb/Law.aspx?lawId=327389&language=ar', 'archive/law/0327/327389.html', '1ee056c5f3cadf8013188a14f1f8f35f25d7be488d4722ac48e70ed59400a4be', '2026-09-25 17:38:46+00', '2026-09-25 17:38:46+00');
INSERT INTO public.source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES ('lawtext', 327389, 'https://legallaw.ul.edu.lb/LawView.aspx?opt=view&LawID=327389', 'archive/lawtext/0327/327389.html', '92de37166ab644a3b725268b58ae46cc9ac0e9cf2e896a7da775c4ba9eee9e22', '2026-09-30 23:44:04+00', '2026-09-30 23:44:04+00');


--
-- PostgreSQL database dump complete
--


