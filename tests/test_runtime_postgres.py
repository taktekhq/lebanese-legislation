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


def test_actual_transactional_restore_idempotence_and_readonly_search(databases):
    dsns, reader, binary, dump_binary, tmp_path = databases
    source, target, bad = dsns
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
