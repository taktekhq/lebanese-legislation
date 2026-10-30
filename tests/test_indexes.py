"""Real query plans on a synthetic corpus, isolated from all source databases."""

import os
import uuid

import psycopg
import pytest
from psycopg import sql

from lebleg_starter.indexes import ensure_search_indexes


@pytest.fixture
def db():
    dsn = os.getenv('LEBLEG_TEST_ADMIN_DSN')
    if not dsn:
        pytest.skip('set LEBLEG_TEST_ADMIN_DSN for disposable PostgreSQL tests')
    name = 'test_indexes_' + uuid.uuid4().hex
    with psycopg.connect(dsn,autocommit=True) as admin:
        admin.execute(sql.SQL('CREATE DATABASE {}').format(sql.Identifier(name)))
    conn = psycopg.connect(dsn,dbname=name)
    try:
        conn.execute('''CREATE TABLE source_documents(
          kind text,source_id integer,part text,lang text,title text,text text,metadata jsonb,
          PRIMARY KEY(kind,source_id,part,lang))''')
        conn.execute('''INSERT INTO source_documents
          SELECT 'law',i,'title','ar','Entry '||i||' '||md5(i::text),
          repeat(md5(i::text)||' ',20),
          jsonb_build_object('identifier',i::text,'issued_on','2020-01-02')
          FROM generate_series(1,30000) i''')
        conn.execute("UPDATE source_documents SET text='A rareNeedle inside original text' WHERE source_id=25000")
        conn.execute("UPDATE source_documents SET title='rareNeedle title' WHERE source_id=29000")
        conn.execute("UPDATE source_documents SET metadata=jsonb_build_object('number','fallback-number','date','2021-02-03') WHERE source_id=29000")
        yield conn
    finally:
        conn.close()
        with psycopg.connect(dsn,autocommit=True) as admin:
            admin.execute(sql.SQL('DROP DATABASE {} WITH (FORCE)').format(sql.Identifier(name)))


def nodes(plan):
    yield plan
    for child in plan.get('Plans',[]):
        yield from nodes(child)


def test_rare_and_absent_literal_queries_use_bitmap_or_and_preserve_results(db):
    contract = ensure_search_indexes(db)
    assert ensure_search_indexes(db) == contract
    db.commit()
    db.execute('ANALYZE source_documents')
    query = "SELECT source_id FROM source_documents WHERE (title ILIKE %s ESCAPE '\\' OR text ILIKE %s ESCAPE '\\') ORDER BY kind,source_id,part,lang LIMIT 8"
    for needle,expected in [('rareNeedle',[25000,29000]),('aDistinctAbsentNeedle',[])]:
        pattern='%'+needle+'%'
        assert [r[0] for r in db.execute(query,(pattern,pattern))] == expected
        plan=db.execute('EXPLAIN (FORMAT JSON) '+query,(pattern,pattern)).fetchone()[0][0]['Plan']
        all_nodes=list(nodes(plan))
        assert any(n['Node Type']=='BitmapOr' for n in all_nodes)
        names={n.get('Index Name') for n in all_nodes}
        assert {'source_documents_title_trgm','source_documents_text_trgm'}<=names


def test_identifier_and_date_fallback_queries_use_expression_indexes(db):
    ensure_search_indexes(db)
    db.commit()
    db.execute('ANALYZE source_documents')
    for expression,value,index in [
        ("coalesce(metadata->>'identifier',metadata->>'number','')",'fallback-number','source_documents_identifier'),
        ("coalesce(metadata->>'issued_on',metadata->>'date','')",'2021-02-03','source_documents_issued_on')]:
        query='SELECT source_id FROM source_documents WHERE '+expression+'=%s'
        assert db.execute(query,(value,)).fetchall()==[(29000,)]
        plan=db.execute('EXPLAIN (FORMAT JSON) '+query,(value,)).fetchone()[0][0]['Plan']
        assert index in {n.get('Index Name') for n in nodes(plan)}, (plan,db.execute("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='source_documents'").fetchall())
