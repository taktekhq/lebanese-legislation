-- Only public snapshot fields enrich source citation and extraction metadata.
UPDATE source_documents d SET metadata=d.metadata || jsonb_strip_nulls(jsonb_build_object(
 'identifier',r.number,'year',r.year,'decided_on',r.decided_on))
FROM ruling r WHERE d.kind='ruling' AND d.source_id=r.id;
UPDATE source_documents d SET metadata=d.metadata || jsonb_strip_nulls(jsonb_build_object(
 'model',p.model,'read_at',p.read_at,'low_confidence',p.low_confidence))
FROM ruling_page p WHERE d.kind='ruling' AND d.source_id=p.ruling_id AND d.part=p.n::text;
UPDATE source_documents d SET metadata=d.metadata || jsonb_strip_nulls(jsonb_build_object('signed_on',a.signed_on))
FROM agreement a WHERE d.kind='agreement' AND d.source_id=a.id;
UPDATE source_documents d SET metadata=d.metadata || jsonb_strip_nulls(jsonb_build_object('published_on',r.published_on))
FROM research r WHERE d.kind='research' AND d.source_id=r.id;
UPDATE source_documents d SET metadata=d.metadata || jsonb_strip_nulls(jsonb_build_object('model',e.model))
FROM legislation_title_en e WHERE d.kind='law' AND d.source_id=e.law_id AND d.part='title' AND d.lang='en';
