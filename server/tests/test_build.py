import os
import uuid

import psycopg
import pytest
from psycopg import sql

from lebleg.build import coverage, ensure_semantic, fill_missing, reuse_vectors
from lebleg.chunks import digest


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
    from lebleg.build import validate_chunks
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


def test_build_refuses_data_or_functions_outside_public(db):
    from lebleg.build import assert_empty_target
    db.execute('CREATE SCHEMA unrelated')
    db.execute('CREATE TABLE unrelated.private_data(secret text)')
    with pytest.raises(ValueError, match='occupied'):
        assert_empty_target(db)
    db.rollback()
    db.execute("CREATE FUNCTION public.private_function() RETURNS text LANGUAGE sql AS $$SELECT 'private'$$")
    with pytest.raises(ValueError, match='occupied'):
        assert_empty_target(db)


def test_real_dump_contains_only_public_whitelist_and_vector_extension(db, tmp_path):
    import subprocess

    from lebleg.build import dump_database
    from lebleg.sources import resource

    dump_bin = os.getenv('LEBLEG_TEST_PGDUMP')
    restore_bin = os.getenv('LEBLEG_TEST_PGRESTORE')
    if not dump_bin or not restore_bin:
        pytest.skip('set LEBLEG_TEST_PGDUMP and LEBLEG_TEST_PGRESTORE to PG18 clients')
    db.execute(resource('public_schema.sql').split('ALTER TABLE',1)[0])
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text)')
    db.execute('CREATE TABLE lebleg_release(release_id text)')
    db.execute('CREATE SCHEMA private')
    db.execute('CREATE TABLE private.secret(value text)')
    db.execute("INSERT INTO private.secret VALUES('must never appear')")
    db.commit()
    destination = tmp_path/'public.dump'
    from psycopg.conninfo import make_conninfo
    dump_database(make_conninfo(os.environ['LEBLEG_TEST_ADMIN_DSN'],dbname=db.info.dbname), destination, dump_bin)
    with destination.open('rb') as stream:
        listing = subprocess.run([restore_bin,'--list'],stdin=stream,capture_output=True,check=True,text=True).stdout
    assert 'EXTENSION - vector' in listing
    assert 'TABLE public legislation ' in listing
    assert 'TABLE public semantic_chunks ' in listing
    assert 'private' not in listing


def test_finalized_release_can_resume_after_dump_failure_without_reloading(db, tmp_path):
    import json
    import subprocess

    from psycopg.conninfo import make_conninfo
    from psycopg.types.json import Jsonb

    from lebleg.build import main
    from lebleg.sources import derive_documents, resource

    dump_bin = os.getenv('LEBLEG_TEST_PGDUMP')
    if not dump_bin:
        pytest.skip('set LEBLEG_TEST_PGDUMP to the PG18 client')
    db.execute(resource('public_schema.sql').split('ALTER TABLE',1)[0])
    derive_documents(db)
    ensure_semantic(db)
    manifest = {'release_id':'test-release','source':{'revision':'a'*40,'repo_id':'taktekhq/lebanese-legislation'},'as_of_date':'2026-10-30',
                'counts':{'source_documents':0,'semantic_chunks':0,'tables':{}}}
    db.execute('CREATE TABLE lebleg_release(release_id text PRIMARY KEY,manifest jsonb NOT NULL)')
    db.execute('INSERT INTO lebleg_release VALUES(%s,%s)',('test-release',Jsonb(manifest)))
    db.commit()
    dsn = make_conninfo(os.environ['LEBLEG_TEST_ADMIN_DSN'],dbname=db.info.dbname)
    argv = ['--database-url',dsn,'--snapshot-dir','does-not-exist','--revision','a'*40,'--release-id','test-release',
            '--as-of-date','2026-10-30','--output-dir',str(tmp_path),'--resume','--embed-missing']
    assert main(argv+['--no-export','--pg-dump','/bin/false']) == 0
    assert not (tmp_path/'corpus.dump').exists()
    with pytest.raises(subprocess.CalledProcessError):
        main(argv+['--pg-dump','/bin/false'])
    assert not (tmp_path/'corpus.dump').exists()
    assert not list(tmp_path.glob('*.partial'))
    assert main(argv+['--pg-dump',dump_bin]) == 0
    assert json.loads((tmp_path/'manifest.json').read_text())['dump']['size_bytes'] > 0


def test_batched_coverage_keeps_reference_boundaries_and_ignores_extra_rows(db):
    ensure_semantic(db)
    db.execute('CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)')
    docs = [('law',i,'مادة','ar','title','نص'+str(i),digest('نص'+str(i))) for i in range(503)]
    with db.cursor() as cur:
        cur.executemany('INSERT INTO source_documents VALUES(%s,%s,%s,%s,%s,%s,%s)',docs)
    vector='['+','.join(['0.1']*768)+']'
    rows=[]
    for sid,model,hashed,excerpt,start in [
        (0,'gemini-embedding-001',digest('نص0'),'نص0',0),
        (499,'gemini-embedding-001',digest('نص499'),'نص499',0),
        (499,'gemini-embedding-001','stale','نص499',0),
        (500,'gemini-embedding-001',digest('نص500'),'wrong',0),
        (501,'gemini-embedding-001',digest('نص501'),'نص501',1),
        (1,'other-model',digest('نص1'),'نص1',0),
        (9999,'gemini-embedding-001',digest('نص0'),'نص0',0)]:
        rows.append((model,'law',sid,'مادة','ar','title',excerpt,hashed,start,start+len(excerpt),vector))
    with db.cursor() as cur:
        cur.executemany('INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)',rows)
    assert coverage(db)=={'expected':503,'indexed':2,'missing':501}
    from lebleg.build import validate_chunks
    with pytest.raises(ValueError,match='canonical'):
        validate_chunks(db)
