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

from lebleg_starter.restore import RestoreError, restore
from lebleg_starter.search import SearchService


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


def prepare_dump(databases):
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


def test_actual_transactional_restore_idempotence_and_readonly_search(databases):
    dsns, reader, binary, _, _ = databases
    _, target, bad = dsns
    m, dump, text = prepare_dump(databases)
    assert restore(m, dump, target, reader, binary) == "restored"
    assert restore(m, dump, target, reader, binary) == "already restored"
    readonly = make_conninfo(**{**conninfo_to_dict(target), "user": reader, "password": "runtime-test-only"})
    service = SearchService(readonly)
    try:
        assert service.health()
        assert service.search("law 123")["passages"][0]["text"] == text
        assert service.search("قانون")["passages"][0]["text"] == text

        class Provider:
            model = "gemini-embedding-001"

            def embed(self, texts, query=False):
                return [[1.0] * 768]

        service.provider = Provider()
        assert service.search("unmatched subject")["passages"][0]["text"] == text
        with psycopg.connect(target) as owner:
            owner.execute("UPDATE semantic_chunks SET hash='stale'")
        assert service.search("unmatched subject")["passages"] == []
        assert service.search("law 999")["passages"] == []
        assert service.search("decree 123")["passages"] == []
        assert service.search("law 123 dated 2021-01-02")["passages"] == []
        with service.pool.connection() as conn, pytest.raises(psycopg.Error):
            conn.execute("DELETE FROM source_documents")
    finally:
        service.close()
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
    import lebleg_starter.restore as module

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


def test_absent_typed_citations_never_call_provider(databases):
    dsns, reader, binary, _, _ = databases
    _, target, _ = dsns
    m, dump, _ = prepare_dump(databases)
    restore(m, dump, target, reader, binary)
    readonly = make_conninfo(**{**conninfo_to_dict(target), "user": reader, "password": "runtime-test-only"})

    class NoProviderCalls:
        model = "gemini-embedding-001"

        def embed(self, *args, **kwargs):
            pytest.fail("explicit citation reached provider")

        def select(self, *args, **kwargs):
            pytest.fail("absent citation reached selector")

    service = SearchService(readonly, NoProviderCalls())
    try:
        for query in [
            "What does the law say about decree 123?",
            "law 123/2005",
            "المرسوم الاشتراعي رقم ١٢٣",
            "قرار مجلس الوزراء رقم 123/2005",
        ]:
            assert service.search(query, select=True)["passages"] == []
        with psycopg.connect(target) as conn:
            conn.execute(
                "UPDATE source_documents SET metadata=metadata||%s",
                (Jsonb({"source_type": "المرسوم الإشتراعي", "year": 2020}),),
            )
        assert service.search("المرسوم الاشتراعي رقم 123/2020")["passages"]
        assert service.search("decree-law 123/2005")["passages"] == []
        with psycopg.connect(target) as conn:
            conn.execute(
                "UPDATE source_documents SET metadata=metadata||%s", (Jsonb({"source_type": "قرار مجلس الوزراء"}),)
            )
        assert service.search("قرار مجلس الوزراء رقم 123/2020")["passages"]
    finally:
        service.close()
