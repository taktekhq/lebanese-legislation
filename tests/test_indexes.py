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
    older_reader = psycopg.connect(dsn,dbname=name)
    try:
        conn.execute('''CREATE TABLE source_documents(
          kind text,source_id integer,part text,lang text,title text,text text,metadata jsonb,
          PRIMARY KEY(kind,source_id,part,lang))''')
        # An unrelated older snapshot must not make the new fixture indexes
        # unusable. Insert final values directly rather than creating HOT chains
        # that legitimately set pg_index.indcheckxmin until old readers finish.
        older_reader.execute('SELECT pg_current_snapshot()')
        conn.execute("""INSERT INTO source_documents
          SELECT 'law',i,'title','ar',
          CASE WHEN i=29000 THEN 'rareNeedle title' ELSE 'Entry '||i||' '||md5(i::text) END,
          CASE WHEN i=25000 THEN 'A rareNeedle inside original text' ELSE repeat(md5(i::text)||' ',20) END,
          CASE WHEN i=29000 THEN jsonb_build_object('number','fallback-number','date','2021-02-03')
          ELSE jsonb_build_object('identifier',i::text,'issued_on','2020-01-02') END
          FROM generate_series(1,30000) i""")
        yield conn
    finally:
        older_reader.close()
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
        assert index in {n.get('Index Name') for n in nodes(plan)}, (plan,db.execute("SELECT c.relname,i.indcheckxmin,i.xmin::text,pg_snapshot_xmin(pg_current_snapshot())::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='source_documents'::regclass").fetchall())
