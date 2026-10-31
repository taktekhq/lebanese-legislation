"""Disposable PostgreSQL integration tests. Never use the source database."""

import hashlib
import os
import subprocess
import uuid

import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict, make_conninfo
from psycopg.types.json import Jsonb
from test_runtime import manifest

from lebleg.restore import RestoreError, restore


@pytest.fixture
def databases(tmp_path):
    dsn = os.getenv("RUNTIME_TEST_DSN")
    binary = os.getenv("PG_RESTORE_BIN")
    dump_binary = os.getenv("PG_DUMP_BIN")
    if not dsn or not binary or not dump_binary:
        pytest.skip("set RUNTIME_TEST_DSN, PG_RESTORE_BIN and PG_DUMP_BIN for disposable PostgreSQL integration")
    prefix = "runtime_" + uuid.uuid4().hex
    names = [prefix + "_source", prefix + "_target", prefix + "_bad"]
    reader = prefix + "_reader"
    with psycopg.connect(dsn, autocommit=True) as admin:
        for name in names:
            admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
        admin.execute(sql.SQL("CREATE ROLE {} LOGIN PASSWORD 'runtime-test-only'").format(sql.Identifier(reader)))
    values = conninfo_to_dict(dsn)
    dsns = [make_conninfo(**{**values, "dbname": name}) for name in names]
    try:
        yield dsns, reader, binary, dump_binary, tmp_path
    finally:
        with psycopg.connect(dsn, autocommit=True) as admin:
            for name in names:
                admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name)))
            admin.execute(sql.SQL("DROP ROLE {}").format(sql.Identifier(reader)))


def prepare_dump(databases, text_search=False, extra_documents=()):
    dsns, _reader, _binary, dump_binary, tmp_path = databases
    source, _target, _bad = dsns
    m = manifest()
    text = "قانون 🙂 نص"
    digest = hashlib.sha256(text.encode()).hexdigest()
    with psycopg.connect(source) as conn:
        conn.execute("CREATE EXTENSION vector")
        conn.execute(
            """CREATE TABLE source_documents(kind text,source_id integer,part text,lang text,title text,text text,hash text,source_url text,metadata jsonb,PRIMARY KEY(kind,source_id,part,lang))"""
        )
        conn.execute(
            """CREATE TABLE semantic_chunks(model text,kind text,source_id integer,part text,lang text,title text,text text,hash text,chunk_start integer,chunk_end integer,embedding halfvec(768),updated_at timestamptz DEFAULT now(),PRIMARY KEY(model,kind,source_id,part,lang,hash,chunk_start))"""
        )
        conn.execute("CREATE INDEX fixture_hnsw ON semantic_chunks USING hnsw(embedding halfvec_cosine_ops)")
        conn.execute("CREATE TABLE lebleg_release(release_id text PRIMARY KEY,manifest jsonb NOT NULL)")
        conn.execute(
            "INSERT INTO source_documents VALUES(%s,1,%s,%s,%s,%s,%s,%s,%s)",
            (
                "law",
                "body",
                "ar",
                "قانون رقم 123",
                text,
                digest,
                "https://example.org/source",
                Jsonb({"identifier": "123", "issued_on": "2020-01-02"}),
            ),
        )
        conn.execute(
            "INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) VALUES(%s,%s,1,%s,%s,%s,%s,%s,0,%s,%s::halfvec)",
            (
                "gemini-embedding-001",
                "law",
                "body",
                "ar",
                "قانون رقم 123",
                text,
                digest,
                len(text),
                "[" + ",".join(["1"] * 768) + "]",
            ),
        )
        for source_id, title, body in extra_documents:
            conn.execute(
                "INSERT INTO source_documents VALUES('law',%s,'body','ar',%s,%s,%s,'https://example.org/source','{}')",
                (source_id, title, body, hashlib.sha256(body.encode()).hexdigest()),
            )
        m["counts"]["source_documents"] += len(extra_documents)
        m["counts"]["tables"]["source_documents"] += len(extra_documents)
        if text_search:
            from lebleg.indexes import ensure_search_indexes

            m["text_search"] = ensure_search_indexes(conn)
        conn.execute("INSERT INTO lebleg_release VALUES(%s,%s)", (m["release_id"], Jsonb(m)))
    dump = tmp_path / "fixture.dump"
    env = os.environ.copy()
    for key, value in conninfo_to_dict(source).items():
        env[
            "PG"
            + {
                "dbname": "DATABASE",
                "password": "PASSWORD",
                "user": "USER",
                "host": "HOST",
                "port": "PORT",
                "sslmode": "SSLMODE",
            }.get(key, key.upper())
        ] = value
    with dump.open("wb") as output:
        subprocess.run(
            [dump_binary, "--format=custom", "--no-owner", "--no-acl"],
            env=env,
            check=True,
            stdout=output,
            stderr=subprocess.PIPE,
        )
    m["dump"]["size_bytes"] = dump.stat().st_size
    m["dump"]["sha256"] = hashlib.sha256(dump.read_bytes()).hexdigest()
    return m, dump, text


def test_actual_transactional_restore_idempotence_and_readonly_reader(databases):
    dsns, reader, binary, _, _ = databases
    _, target, bad = dsns
    m, dump, text = prepare_dump(databases)
    assert restore(m, dump, target, reader, binary) == "restored"
    assert restore(m, dump, target, reader, binary) == "already restored"
    readonly = make_conninfo(**{**conninfo_to_dict(target), "user": reader, "password": "runtime-test-only"})
    # The restored database is readable, and read-only, for the reader role the services use.
    with psycopg.connect(readonly) as conn:
        assert conn.execute("SELECT text FROM source_documents").fetchone()[0] == text
        with pytest.raises(psycopg.Error):
            conn.execute("DELETE FROM source_documents")
    with psycopg.connect(bad) as conn:
        conn.execute("CREATE TABLE unrelated(id integer)")
    with pytest.raises(RestoreError):
        restore(m, dump, bad, reader, binary)


def test_failed_restore_rolls_back_all_imported_tables(databases):
    dsns, reader, binary, dump_binary, tmp_path = databases
    source, target, _ = dsns
    with psycopg.connect(source) as conn:
        conn.execute("CREATE TABLE earlier(id integer)")
        conn.execute("INSERT INTO earlier SELECT generate_series(1,10000)")
    dump = tmp_path / "broken.dump"
    env = os.environ.copy()
    for key, value in conninfo_to_dict(source).items():
        env[
            "PG"
            + {
                "dbname": "DATABASE",
                "password": "PASSWORD",
                "user": "USER",
                "host": "HOST",
                "port": "PORT",
                "sslmode": "SSLMODE",
            }.get(key, key.upper())
        ] = value
    with dump.open("wb") as output:
        subprocess.run(
            [dump_binary, "--format=custom", "--no-owner", "--no-acl"],
            env=env,
            check=True,
            stdout=output,
            stderr=subprocess.PIPE,
        )
    # Truncate the data stream after the custom archive header and TOC.
    dump.write_bytes(dump.read_bytes()[:-128])
    m = manifest()
    m["dump"]["size_bytes"] = dump.stat().st_size
    m["dump"]["sha256"] = hashlib.sha256(dump.read_bytes()).hexdigest()
    with pytest.raises(RestoreError):
        restore(m, dump, target, reader, binary)
    with psycopg.connect(target) as conn:
        assert conn.execute("SELECT to_regclass('earlier')").fetchone()[0] is None
        assert conn.execute("SELECT to_regclass('restore_attempt')").fetchone()[0] is not None
    with pytest.raises(RestoreError, match="transactional restore failed"):
        restore(m, dump, target, reader, binary)


def test_missing_reader_is_rejected_before_import(databases):
    dsns, reader, binary, _, _ = databases
    _, target, _ = dsns
    m, dump, _ = prepare_dump(databases)
    with pytest.raises(RestoreError, match="reader"):
        restore(m, dump, target, reader + "_absent", binary)
    with psycopg.connect(target) as conn:
        assert conn.execute("SELECT to_regclass('source_documents')").fetchone()[0] is None
        assert conn.execute("SELECT to_regclass('restore_attempt')").fetchone()[0] is None
    assert restore(m, dump, target, reader, binary) == "restored"


@pytest.mark.parametrize("failure", ["interruption", "validation"])
def test_post_import_interruption_resumes_without_import(monkeypatch, databases, failure):
    import lebleg.restore as module

    dsns, reader, binary, _, _ = databases
    _, target, _ = dsns
    m, dump, _ = prepare_dump(databases)
    if failure == "interruption":
        original = module._finish_restore

        def interrupted(*args, **kwargs):
            raise KeyboardInterrupt()

        monkeypatch.setattr(module, "_finish_restore", interrupted)
        with pytest.raises(KeyboardInterrupt):
            restore(m, dump, target, reader, binary)
        monkeypatch.setattr(module, "_finish_restore", original)
    else:
        original = module.check_database

        def invalid(*args, **kwargs):
            raise RestoreError("temporary validation failure")

        monkeypatch.setattr(module, "check_database", invalid)
        with pytest.raises(RestoreError, match="temporary"):
            restore(m, dump, target, reader, binary)
        monkeypatch.setattr(module, "check_database", original)
    with psycopg.connect(target) as conn:
        assert conn.execute("SELECT count(*) FROM source_documents").fetchone()[0] == 1
        assert conn.execute("SELECT to_regclass('restore_receipt')").fetchone()[0] is None
    wrong = {**m, "build_version": "different"}
    with pytest.raises(RestoreError, match="occupied"):
        restore(wrong, dump, target, reader, binary)
    run = module.subprocess.run

    def no_import(args, **kwargs):
        assert args == [binary, "--version"]
        return run(args, **kwargs)

    monkeypatch.setattr(module.subprocess, "run", no_import)
    assert restore(m, dump, target, reader, binary) == "restored"



def test_restore_checks_declared_text_search_extension_and_indexes(databases):
    dsns, reader, binary, _, _ = databases
    _, target, _ = dsns
    m, dump, _ = prepare_dump(databases, text_search=True)
    assert restore(m, dump, target, reader, binary) == "restored"
    with psycopg.connect(target) as conn:
        conn.execute("DROP INDEX source_documents_title_trgm")
    with pytest.raises(RestoreError, match="text search index"):
        restore(m, dump, target, reader, binary)



def test_finalize_analyze_runs_without_grant_transaction_or_xid_lock(databases):
    from psycopg.pq import TransactionStatus

    from lebleg.restore import _finish_restore

    dsns,reader,_binary,_dump_binary,_tmp_path=databases
    source,_target,_bad=dsns
    m,_dump,text=prepare_dump(databases)
    observed=[]
    with psycopg.connect(source,autocommit=True) as conn, psycopg.connect(source,autocommit=True) as observer:
        class Connection:
            def transaction(self):
                return conn.transaction()

            def execute(self,query,params=None):
                if query=='ANALYZE':
                    observed.append(conn.info.transaction_status)
                    assert conn.info.transaction_status==TransactionStatus.IDLE
                    assert not observer.execute("""SELECT EXISTS(SELECT 1 FROM pg_locks
                        WHERE pid=%s AND locktype='transactionid' AND mode='ExclusiveLock' AND granted)""",
                        (conn.info.backend_pid,)).fetchone()[0]
                return conn.execute(query,params)

        _finish_restore(Connection(),m,reader)
        assert observed==[TransactionStatus.IDLE]
        assert conn.execute('SELECT manifest FROM restore_receipt').fetchone()[0]==m
        assert conn.execute('SELECT text FROM source_documents').fetchone()[0]==text
        assert conn.execute("SELECT has_table_privilege(%s,'source_documents','SELECT'),has_table_privilege(%s,'source_documents','INSERT,UPDATE,DELETE')",(reader,reader)).fetchone()==(True,False)
