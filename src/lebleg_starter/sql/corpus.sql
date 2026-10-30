WITH articles AS (SELECT DISTINCT ON (f.law_id,f.number) f.law_id,f.number,f.text_ar,
 CASE WHEN f.text_ar=a.text_ar THEN coalesce(a.text_fr,'') ELSE '' END AS text_fr,l.title
 FROM article_in_force f JOIN legislation l ON l.id=f.law_id
 LEFT JOIN legislation_article a ON a.law_id=f.law_id AND a.position=f.position
 WHERE f.state='in_force' AND NOT f.pending AND l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
 AND f.text_ar IS NOT NULL AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
 ORDER BY f.law_id,f.number,f.position NULLS LAST)
 SELECT 'article' AS kind,law_id AS source_id,number AS part,'ar' AS lang,title,text_ar AS body FROM articles
 UNION ALL SELECT 'article',law_id,number,'fr',title,text_fr FROM articles WHERE text_fr<>''
 UNION ALL SELECT 'law',l.id,v.part,'ar',l.title,v.body FROM legislation l
 CROSS JOIN LATERAL (VALUES ('title',l.title),('preamble',l.preamble),('explanatory_note',l.explanatory_note),('closing',l.closing)) v(part,body)
 WHERE l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE) AND coalesce(v.body,'')<>''
 UNION ALL SELECT 'law',l.id,'title','en',e.title_en,e.title_en FROM legislation_title_en e JOIN legislation l ON l.id=e.law_id
 WHERE l.status<>'repealed' AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
 UNION ALL SELECT 'ruling',id,'','ar',title,coalesce(text_ar,title) FROM ruling
 UNION ALL SELECT 'ruling',p.ruling_id,p.n::text,'ar',r.title,p.text_ar FROM ruling_page p JOIN ruling r ON r.id=p.ruling_id
 UNION ALL SELECT 'opinion',id,'','ar',coalesce(subject,''),coalesce(text_ar,subject,'') FROM opinion
 UNION ALL SELECT 'agreement',id,'','ar',title,coalesce(text_ar,title) FROM agreement
 UNION ALL SELECT 'research',id,'','ar',title,coalesce(text_ar,title) FROM research
 UNION ALL SELECT 'doctrine',id,'','ar',title,coalesce(abstract,title) FROM doctrine_article
