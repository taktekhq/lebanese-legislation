from datetime import date

from test_build import db as db  # noqa: PLC0414

from lebleg_starter.sources import source_details


def test_urls_and_unofficial_english_metadata():
    url, metadata = source_details('law', 10, 'title', 'en', {'kind':'مرسوم', 'number':'12', 'issued_on':date(2020,1,2)})
    assert url == 'https://lebaneselegislation.com/decree/10/'
    assert metadata['english_unofficial'] is True
    assert metadata['issued_on'] == '2020-01-02'
    assert source_details('article', 10, '3', 'ar', {'kind':'قانون'})[0].endswith('/law/10/article/3/')


def test_real_typed_parquet_and_source_eligibility(db, tmp_path):
    from datetime import UTC, datetime

    import pyarrow as pa
    import pyarrow.parquet as pq

    from lebleg_starter.sources import derive_documents, import_parquet, resource

    # Generate a wholly synthetic public snapshot using the frozen typed contract.
    db.execute(resource('public_schema.sql').split('ALTER TABLE', 1)[0])
    definitions = db.execute("SELECT table_name,column_name,udt_name FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position").fetchall()
    type_map = {'text':pa.string(),'int2':pa.int16(),'int4':pa.int32(),'int8':pa.int64(),
                'float4':pa.float32(),'date':pa.date32(),'timestamptz':pa.timestamp('us',tz='UTC'),
                'bool':pa.bool_(),'_text':pa.list_(pa.string()),'_int4':pa.list_(pa.int32())}
    schemas = {}
    for table, column, typ in definitions:
        schemas.setdefault(table, []).append(pa.field(column,type_map[typ]))
    db.rollback()
    now = datetime(2020,1,2,tzinfo=UTC)
    samples = {
        'legislation_kind':[{'name_ar':'قانون'}],
        'legislation':[{'id':1,'kind':'قانون','title':'عنوان','status':'in_force','issued_on':date(2020,1,2)},
                       {'id':2,'kind':'قانون','title':'ملغى','status':'repealed'},
                       {'id':3,'kind':'قانون','title':'لاحق','status':'in_force','issued_on':date(2099,1,1)}],
        'legislation_title_en':[{'law_id':1,'title_en':'Unofficial title','model':'public-title-model'}],
        'legislation_article':[{'law_id':1,'position':1,'number':'1','text_ar':'أصلي','text_fr':'original français'}],
        'article_in_force':[{'law_id':1,'position':1,'number':'1','text_ar':'معدّل','state':'in_force','pending':False}],
        'doctrine_article':[{'id':9,'title':'بحث','authors':['أ','B']}],
        'source_page':[{'doc_type':'law','centre_id':1,'first_seen':now}],
        'ruling':[{'id':8,'title':'حكم','number':'7','year':2020,'decided_on':date(2020,1,2)}],
        'ruling_page':[{'ruling_id':8,'n':1,'text_ar':'نص','model':'public-extractor','read_at':now,'low_confidence':True}],
    }
    for table, fields in schemas.items():
        pq.write_table(pa.Table.from_pylist(samples.get(table, []),schema=pa.schema(fields)),tmp_path / (table+'.parquet'))
    counts = import_parquet(db,tmp_path)
    assert len(counts) == 29
    assert db.execute('SELECT issued_on FROM legislation WHERE id=1').fetchone()[0] == date(2020,1,2)
    assert db.execute('SELECT authors FROM doctrine_article').fetchone()[0] == ['أ','B']
    assert db.execute('SELECT first_seen FROM source_page').fetchone()[0] == now
    assert derive_documents(db) == 6  # Titles, article, doctrine, ruling and its public machine-read page.
    rows = db.execute('SELECT kind,source_id,part,lang,text,metadata FROM source_documents ORDER BY kind,lang').fetchall()
    assert not any(r[3]=='fr' or r[1] in (2,3) for r in rows)
    assert next(r for r in rows if r[3]=='en')[5]['english_unofficial'] is True
    assert next(r for r in rows if r[3]=='en')[5]['model'] == 'public-title-model'
    page = next(r for r in rows if r[0]=='ruling' and r[2]=='1')[5]
    assert page['identifier']=='7' and page['decided_on']=='2020-01-02'
    assert page['low_confidence'] is True and page['model']=='public-extractor'


def test_arrow_contract_rejects_narrowing_float_and_changed_timestamp():
    import pyarrow as pa
    import pytest

    from lebleg_starter.sources import validate_arrow_type
    validate_arrow_type(pa.float32(),'float4')
    validate_arrow_type(pa.list_(pa.int32()),'_int4')
    with pytest.raises(ValueError,match='type'):
        validate_arrow_type(pa.float64(),'float4')
    with pytest.raises(ValueError,match='type'):
        validate_arrow_type(pa.timestamp('us'),'timestamptz')


def test_article_source_url_uses_the_site_unicode_whitespace_slug():
    url,_ = source_details('article',10,'1 مكرر','ar',{'kind':'قانون'})
    assert url == 'https://lebaneselegislation.com/law/10/article/1-%D9%85%D9%83%D8%B1%D8%B1/'
