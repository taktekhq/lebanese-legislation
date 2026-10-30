"""Derived wording must never change a quotation or claim stale applicability."""
from copy import deepcopy
from datetime import date

import pytest

from lebleg_starter import sources
from lebleg_starter.chunks import digest


def record():
    text = 'مئة مليون ليرة'
    obj = {'kind': 'statutory_monetary_adjustment', 'text_ar': 'خمسة مليارات (5,000,000,000) ليرة',
               'source_text_ar': text, 'source_sha256': digest(text), 'is_published_wording': False, 'multiplier': 50,
               'authority': {'law_id': 330819, 'article': '1', 'url': 'https://lebaneselegislation.com/decree/330819/article/1/', 'effective_on': '2024-09-19'}}
    return {'law_id': 244565, 'number': '86', 'position': 86, 'source_sha256': digest(text), 'calculated_current': obj}


def test_validate_calculated_current_exact_source_and_authority():
    row = record()
    assert sources.validate_calculated_current(row, row['calculated_current']['source_text_ar'], date(2026,10,30)) == row['calculated_current']


@pytest.mark.parametrize('field,value', [('kind','other'),('multiplier',True),('is_published_wording',True),('source_sha256','bad'),('source_text_ar','stale'),('text_ar','')])
def test_rejects_invalid_calculation_contract(field, value):
    row = record()
    row['calculated_current'][field] = value
    with pytest.raises(ValueError, match='calculated'):
        sources.validate_calculated_current(row, record()['calculated_current']['source_text_ar'], date(2026,10,30))


def test_rejects_other_law_wrong_authority_and_future():
    for change in ('law', 'authority', 'future'):
        row = deepcopy(record())
        if change == 'law':
            row['law_id'] = 1
        elif change == 'authority':
            row['calculated_current']['authority']['article'] = '2'
        with pytest.raises(ValueError, match='calculated'):
            sources.validate_calculated_current(row, row['calculated_current']['source_text_ar'], date(2020,1,1) if change == 'future' else date(2026,10,30))


def test_literal_calculated_lookup_preserves_original_passage():
    from contextlib import contextmanager

    from test_runtime import manifest
    from test_search_runtime import Result, source_row

    from lebleg_starter.search import SearchService
    row = source_row(244565, record()['calculated_current']['source_text_ar'])
    row.update(kind='article', part='86', title='المادة 86', metadata={'calculated_current':record()['calculated_current']})
    class Connection:
        def execute(self, query, params=None):
            if 'SELECT r.manifest' in query:
                return Result([{'manifest':manifest()}])
            if 'title ILIKE' in query and 'AND NOT' not in query:
                return Result([])
            assert "metadata->'calculated_current'->>'text_ar'" in query
            return Result([row])
    @contextmanager
    def connection():
        yield Connection()
    service = SearchService.__new__(SearchService)
    service.connection,service.provider=connection,None
    passage = service.search('5,000,000,000',semantic=False)['passages'][0]
    assert passage['text'] == row['text']
    assert passage['metadata']['calculated_current'] == record()['calculated_current']


from test_build import db as db  # noqa: PLC0414


def write_snapshot(db, path, row=None):
    import json

    import pyarrow as pa
    import pyarrow.parquet as pq
    db.execute(sources.resource('public_schema.sql').split('ALTER TABLE',1)[0])
    columns=db.execute("SELECT table_name,column_name,udt_name FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position").fetchall()
    types={'text':pa.string(),'jsonb':pa.string(),'int2':pa.int16(),'int4':pa.int32(),'int8':pa.int64(),'float4':pa.float32(),'date':pa.date32(),'timestamptz':pa.timestamp('us',tz='UTC'),'bool':pa.bool_(),'_text':pa.list_(pa.string()),'_int4':pa.list_(pa.int32())}
    schemas={}
    for table,col,typ in columns:
        schemas.setdefault(table,[]).append(pa.field(col,types[typ]))
    db.rollback()
    raw=record()['calculated_current']['source_text_ar']
    samples={'legislation_kind':[{'name_ar':'مرسوم اشتراعي'}],
             'legislation':[{'id':244565,'kind':'مرسوم اشتراعي','title':'أصول المحاكمات','status':'in_force','issued_on':date(1983,1,1)}],
             'legislation_article':[{'law_id':244565,'number':'86','position':86,'text_ar':raw}],
             'article_in_force':[{'law_id':244565,'number':'86','position':86,'text_ar':raw,'state':'in_force','pending':False,'valid_from':date(2000,1,1)}]}
    if row:
        samples['article_calculated_current']=[{**row,'calculated_current':json.dumps(row['calculated_current'])}]
    for table,fields in schemas.items():
        if table=='article_calculated_current' and row is None:
            continue
        pq.write_table(pa.Table.from_pylist(samples.get(table,[]),schema=pa.schema(fields)),path/(table+'.parquet'))


@pytest.mark.parametrize('include_counts', [False, True])
def test_real_optional_sidecar_and_metadata_preserve_original(db,tmp_path,include_counts):
    row=record()
    write_snapshot(db,tmp_path,row)
    assert sources.import_parquet(db,tmp_path)['article_calculated_current']==1
    sources.derive_documents(db,date(2026,10,30))
    text,hashed,metadata=db.execute("SELECT text,hash,metadata FROM source_documents WHERE kind='article'").fetchone()
    assert text==row['calculated_current']['source_text_ar']
    assert hashed==row['source_sha256']
    assert metadata['calculated_current']==row['calculated_current']
    from contextlib import contextmanager

    from psycopg.rows import dict_row
    from psycopg.types.json import Jsonb
    from test_runtime import manifest

    from lebleg_starter.search import SearchService
    db.execute('CREATE TABLE lebleg_release(release_id text,manifest jsonb)')
    db.execute('CREATE TABLE restore_receipt(singleton boolean,manifest jsonb)')
    m=manifest()
    db.execute('INSERT INTO lebleg_release VALUES(%s,%s)',(m['release_id'],Jsonb(m)))
    db.execute('INSERT INTO restore_receipt VALUES(true,%s)',(Jsonb(m),))
    @contextmanager
    def connection():
        with db.cursor(row_factory=dict_row) as cursor:
            yield cursor
    service=SearchService.__new__(SearchService)
    service.connection,service.provider=connection,None
    result=service.search('5,000,000,000',semantic=False,kinds=['article'],year=1983,include_counts=include_counts)
    passage=result['passages'][0]
    if include_counts:
        assert result['exact_counts']=={'article':1}
    assert service.search('5,000,000,000',semantic=False,kinds=['article'],year=1984,include_counts=include_counts)['passages']==[]
    later=service.search('5,000,000,000',semantic=False,kinds=['article'],year=1983,offset=1,include_counts=include_counts)
    assert later['passages']==[]
    if include_counts:
        assert later['exact_counts']=={'article':1}
    assert passage['text']==text
    assert passage['source_hash']==hashed
    assert passage['metadata']['calculated_current']==row['calculated_current']


def test_real_old_snapshot_has_no_derived_claims(db,tmp_path):
    write_snapshot(db,tmp_path)
    assert sources.import_parquet(db,tmp_path)['article_calculated_current']==0
    sources.derive_documents(db)
    assert db.execute("SELECT count(*) FROM source_documents WHERE metadata ? 'calculated_current'").fetchone()[0]==0


@pytest.mark.parametrize('mutation', ['hash','position','pending','state','future','annulled','number'])
def test_real_rejects_ineligible_sidecar(db,tmp_path,mutation):
    import pyarrow as pa
    import pyarrow.parquet as pq
    row=record()
    if mutation=='hash':
        row['source_sha256']='bad'
    if mutation=='position':
        row['position']=87
    if mutation=='number':
        row['number']='87'
    write_snapshot(db,tmp_path,row)
    if mutation in ('pending','state','future'):
        path=tmp_path/'article_in_force.parquet'
        table=pq.read_table(path)
        rows=table.to_pylist()
        rows[0].update({'pending':{'pending':True},'state':{'state':'repealed'},'future':{'valid_from':date(2099,1,1)}}[mutation])
        pq.write_table(pa.Table.from_pylist(rows,schema=table.schema),path)
    if mutation=='annulled':
        path=tmp_path/'legislation.parquet'
        table=pq.read_table(path)
        rows=table.to_pylist()
        rows[0]['status']='annulled'
        pq.write_table(pa.Table.from_pylist(rows,schema=table.schema),path)
    with pytest.raises(ValueError,match='calculated'):
        sources.import_parquet(db,tmp_path)


@pytest.mark.parametrize('include_counts', [False, True])
def test_literal_derived_scope_preserves_other_laws_raw_matches(db,tmp_path,include_counts):
    from contextlib import contextmanager

    from psycopg.rows import dict_row
    from psycopg.types.json import Jsonb
    from test_runtime import manifest

    from lebleg_starter.search import SearchService
    row=record()
    write_snapshot(db,tmp_path,row)
    sources.import_parquet(db,tmp_path)
    sources.derive_documents(db)
    # A calculated view belongs only to original Arabic article records in its
    # statutory scope. Other resources may still match their own raw wording.
    for kind,sid,lang,text,metadata in [
        ('article',1,'ar','ordinary',{'calculated_current':row['calculated_current']}),
        ('article',244565,'fr','ordinary',{'calculated_current':row['calculated_current']}),
        ('article',244565,'en','ordinary',{'calculated_current':row['calculated_current']}),
        ('law',244565,'ar','ordinary',{'calculated_current':row['calculated_current']}),
        ('article',99,'ar','المبلغ 5,000,000,000 ليرة',{}),
    ]:
        db.execute('INSERT INTO source_documents VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s)',
                   (kind,sid,'86',lang,'عنوان',text,digest(text),'https://example.org/source',Jsonb(metadata)))
    db.execute('CREATE TABLE lebleg_release(release_id text,manifest jsonb)')
    db.execute('CREATE TABLE restore_receipt(singleton boolean,manifest jsonb)')
    m=manifest()
    db.execute('INSERT INTO lebleg_release VALUES(%s,%s)',(m['release_id'],Jsonb(m)))
    db.execute('INSERT INTO restore_receipt VALUES(true,%s)',(Jsonb(m),))
    @contextmanager
    def connection():
        with db.cursor(row_factory=dict_row) as cursor:
            yield cursor
    service=SearchService.__new__(SearchService)
    service.connection,service.provider=connection,None
    result=service.search('5,000,000,000',limit=50,semantic=False,include_counts=include_counts)
    assert {(p['kind'],p['source_id'],p['lang']) for p in result['passages']}=={('article',244565,'ar'),('article',99,'ar')}
    if include_counts:
        assert result['exact_counts']=={'article':2}
