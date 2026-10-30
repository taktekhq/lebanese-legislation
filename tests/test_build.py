import os
import uuid

import psycopg
import pytest
from psycopg import sql

from lebleg_starter.build import coverage, ensure_semantic, fill_missing, reuse_vectors
from lebleg_starter.chunks import digest


@pytest.fixture
def db():
    dsn = os.getenv('LEBLEG_TEST_ADMIN_DSN')
    if not dsn:
        pytest.skip('set LEBLEG_TEST_ADMIN_DSN for disposable real PostgreSQL tests')
    name = 'test_build_' + uuid.uuid4().hex
    with psycopg.connect(dsn, autocommit=True) as admin:
        admin.execute(sql.SQL('CREATE DATABASE {}').format(sql.Identifier(name)))
    conn = psycopg.connect(dsn, dbname=name)
    try:
        yield conn
    finally:
        conn.close()
        with psycopg.connect(dsn, autocommit=True) as admin:
            admin.execute(sql.SQL('DROP DATABASE {} WITH (FORCE)').format(sql.Identifier(name)))


def test_real_reuse_excludes_stale_private_and_replaces_title(db):
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    text = 'أ🙂' * 10
    db.execute('INSERT INTO source_documents VALUES(%s,1,\'\',\'ar\',\'public title\',%s,%s)', ('law', text, digest(text)))
    # Source schema represents a read-only remote connection in production.
    db.execute('CREATE SCHEMA saved')
    db.execute('CREATE TABLE saved.semantic_chunks (LIKE public.semantic_chunks INCLUDING ALL)')
    vector = '[' + ','.join(['0.1']*768) + ']'
    for kind, hashed, excerpt in [('law',digest(text),text), ('topic',digest(text),text), ('law','stale',text)]:
        db.execute('INSERT INTO saved.semantic_chunks VALUES(%s,%s,1,\'\',\'ar\',\'PRIVATE TITLE\',%s,%s,0,20,%s,now())', ('gemini-embedding-001',kind,excerpt,hashed,vector))
    db.commit()
    assert reuse_vectors(db, db, source_table='saved.semantic_chunks') == 1
    assert db.execute('SELECT title FROM semantic_chunks').fetchall() == [('public title',)]
    assert coverage(db) == {'expected':1,'indexed':1,'missing':0}


def test_cap_rejected_before_provider_calls(db):
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    db.execute("INSERT INTO source_documents VALUES('law',1,'','ar','title','text',%s)", (digest('text'),))
    class Provider:
        def embed(self, texts, query=False):
            raise AssertionError('provider must not be called')
    with pytest.raises(ValueError, match='cap'):
        fill_missing(db, Provider(), cap=0)


def test_final_validation_rejects_wrong_boundary_and_orphan(db):
    from lebleg_starter.build import validate_chunks
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    db.execute("INSERT INTO source_documents VALUES('law',1,'','ar','title','text',%s)", (digest('text'),))
    vector = '[' + ','.join(['0.1'] * 768) + ']'
    db.execute("INSERT INTO semantic_chunks VALUES(%s,'law',1,'','ar','title','ext',%s,1,4,%s,now())", ('gemini-embedding-001',digest('text'),vector))
    with pytest.raises(ValueError, match='canonical'):
        validate_chunks(db)


def test_paid_batches_checkpoint_before_a_later_provider_failure(db):
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    text = 'أ' * 26000
    db.execute("INSERT INTO source_documents VALUES('law',1,'','ar','title',%s,%s)", (text,digest(text)))
    db.commit()
    class Provider:
        calls = 0
        def embed(self, texts, query=False):
            self.calls += 1
            if self.calls == 2:
                raise RuntimeError('temporary provider failure')
            return [[0.1]*768 for _ in texts]
    with pytest.raises(RuntimeError, match='temporary'):
        fill_missing(db, Provider(), cap=100, checkpoint=True)
    db.rollback()
    assert db.execute('SELECT count(*) FROM semantic_chunks').fetchone()[0] == 32


def test_coverage_whitespace_has_no_expected_chunks(db):
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    db.execute("INSERT INTO source_documents VALUES('law',1,'','ar','title',%s,%s)", ('\t\n',digest('\t\n')))
    assert coverage(db) == {'expected':0,'indexed':0,'missing':0}
