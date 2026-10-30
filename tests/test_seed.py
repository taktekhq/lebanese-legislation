"""No-network vector reuse against three disposable PostgreSQL databases."""

import hashlib
import json
import os
import uuid

import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.types.json import Jsonb

from lebleg_starter.chunks import MODEL, chunks
from lebleg_starter.seed import SeedError, main, seed
from lebleg_starter.sources import resource


def vector(axis=0):
    return "[" + ",".join("1" if i == axis else "0" for i in range(768)) + "]"


def fingerprint(text):
    return hashlib.sha256(text.encode()).hexdigest()


@pytest.fixture
def databases():
    admin_dsn = os.getenv("SEED_TEST_ADMIN_DSN") or os.getenv("LEBLEG_TEST_ADMIN_DSN")
    if not admin_dsn:
        pytest.skip("set SEED_TEST_ADMIN_DSN for disposable seed integration")
    names = ["seed_" + uuid.uuid4().hex + suffix for suffix in ("_source", "_canonical", "_target")]
    with psycopg.connect(admin_dsn, autocommit=True) as admin:
        for name in names:
            admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
    dsns = [make_conninfo(admin_dsn, dbname=name) for name in names]
    try:
        for dsn in (dsns[0], dsns[2]):
            with psycopg.connect(dsn) as conn:
                conn.execute(resource("semantic.sql"))
        with psycopg.connect(dsns[0]) as conn:
            conn.execute("CREATE TABLE lebleg_release(release_id text PRIMARY KEY,manifest jsonb NOT NULL)")
            embedding = {
                "model": MODEL,
                "dimensions": 768,
                "task_type": "RETRIEVAL_DOCUMENT",
                "chunk_size": 900,
                "chunk_overlap": 120,
                "storage": "halfvec(768)",
            }
            conn.execute("INSERT INTO lebleg_release VALUES(%s,%s)", ("synthetic", Jsonb({"embedding": embedding})))
        with psycopg.connect(dsns[1]) as conn:
            conn.execute(
                "CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,PRIMARY KEY(kind,source_id,part,lang))"
            )
        with psycopg.connect(dsns[2]) as conn:
            conn.execute(
                "INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) SELECT %s,'topic',n,'private','ar','private topic','private',%s,0,7,%s::halfvec FROM generate_series(1,381) n",
                (MODEL, fingerprint("private"), vector(1)),
            )
            conn.execute(
                "INSERT INTO semantic_documents VALUES(%s,'topic',1,'private','ar',%s,'private-generation')",
                (MODEL, fingerprint("private")),
            )
        yield dsns
    finally:
        with psycopg.connect(admin_dsn, autocommit=True) as admin:
            for name in names:
                admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name)))


def document(dsns, sid, text, title="canonical title", source_text=None, source_vector=None):
    with psycopg.connect(dsns[1]) as canonical:
        canonical.execute("INSERT INTO source_documents VALUES('ruling',%s,'1','ar',%s,%s)", (sid, title, text))
    body = text if source_text is None else source_text
    with psycopg.connect(dsns[0]) as source:
        for chunk in chunks(body):
            source.execute(
                "INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) VALUES(%s,'ruling',%s,'1','ar','public title',%s,%s,%s,%s,%s::halfvec)",
                (MODEL, sid, chunk.text, chunk.hash, chunk.start, chunk.end, source_vector or vector()),
            )


def test_seed_canonical_titles_skips_existing_and_preserves_topics(databases):
    document(databases, 1, "النص 🙂")
    document(databases, 2, "محتوى آخر", title="current canonical title")
    document(databases, 3, "تصحيح النص")
    with psycopg.connect(databases[2]) as target:
        for sid, body, stored in [(1, "النص 🙂", "النص 🙂"), (3, "تصحيح النص", "corrupted")]:
            target.execute(
                "INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding,updated_at) VALUES(%s,'ruling',%s,'1','ar','existing title',%s,%s,0,%s,%s::halfvec,'2020-01-01')",
                (MODEL, sid, stored, fingerprint(body), len(body), vector(1)),
            )
    result = seed(*databases, group_size=2)
    assert result["documents"] == 3 and result["skipped"] == 1 and result["seeded"] == 2
    with psycopg.connect(databases[2]) as target:
        assert target.execute(
            "SELECT title,embedding::text,updated_at::date::text FROM semantic_chunks WHERE kind='ruling' AND source_id=1"
        ).fetchone() == ("existing title", vector(1), "2020-01-01")
        assert target.execute(
            "SELECT title,text FROM semantic_chunks WHERE kind='ruling' AND source_id=2"
        ).fetchone() == ("current canonical title", "محتوى آخر")
        assert (
            target.execute("SELECT text FROM semantic_chunks WHERE kind='ruling' AND source_id=3").fetchone()[0]
            == "تصحيح النص"
        )
        assert target.execute("SELECT count(*) FROM semantic_chunks WHERE kind='topic'").fetchone()[0] == 381
        assert target.execute("SELECT seen_run FROM semantic_documents").fetchall() == [("private-generation",)]
    assert seed(*databases)["skipped"] == 3


def test_seed_rejects_stale_sources_and_zero_vectors(databases):
    document(databases, 1, "current source", source_text="older source")
    document(databases, 2, "correct source", source_vector="[" + ",".join(["0"] * 768) + "]")
    document(databases, 3, "🙂" * 901)
    with psycopg.connect(databases[0]) as source:
        source.execute("UPDATE semantic_chunks SET text='wrong' WHERE source_id=3 AND chunk_start=780")
    result = seed(*databases)
    assert result["seeded"] == 1 and result["unavailable"] == 1 and result["mismatched"] == 2
    with psycopg.connect(databases[2]) as target:
        assert target.execute(
            "SELECT source_id,chunk_start,chunk_end FROM semantic_chunks WHERE kind='ruling'"
        ).fetchall() == [(3, 0, 900)]


def test_seed_skips_target_before_querying_source(monkeypatch, databases):
    import lebleg_starter.seed as module

    document(databases, 1, "unchanged")
    assert seed(*databases)["seeded"] == 1

    def forbidden(*args, **kwargs):
        pytest.fail("unchanged target queried source vectors")

    monkeypatch.setattr(module, "_read_candidates", forbidden)
    assert seed(*databases)["skipped"] == 1


def test_source_contract_fails_closed_before_any_target_writes(databases):
    document(databases, 1, "source")
    with psycopg.connect(databases[0]) as source:
        source.execute(
            "UPDATE lebleg_release SET manifest=jsonb_set(manifest,'{embedding,model}',%s)", (Jsonb("wrong-model"),)
        )
    with pytest.raises(SeedError, match="contract"):
        seed(*databases)
    with psycopg.connect(databases[2]) as target:
        assert target.execute("SELECT count(*) FROM semantic_chunks WHERE kind='ruling'").fetchone()[0] == 0


def test_group_failure_rolls_back_only_current_group_and_resumes(monkeypatch, databases):
    import lebleg_starter.seed as module

    for sid in range(3):
        document(databases, sid, "source " + str(sid))
    original = module._write_group
    calls = []

    def broken(target, rows):
        original(target, rows)
        calls.append(1)
        if len(calls) == 2:
            raise psycopg.DataError("postgresql://owner:SECRET@host/db")

    monkeypatch.setattr(module, "_write_group", broken)
    with pytest.raises(SeedError) as error:
        seed(*databases, group_size=1)
    assert "SECRET" not in str(error.value)
    with psycopg.connect(databases[2]) as target:
        assert target.execute("SELECT source_id FROM semantic_chunks WHERE kind='ruling'").fetchall() == [(0,)]
    monkeypatch.setattr(module, "_write_group", original)
    result = seed(*databases, group_size=1)
    assert result["skipped"] == 1 and result["seeded"] == 2


def test_readonly_canonical_query_and_bounded_cli(monkeypatch, databases, tmp_path, capsys):
    import lebleg_starter.seed as module

    for sid in range(3):
        document(databases, sid, "source " + str(sid))
    read = module._read_candidates

    def checked(source, pending):
        assert source.execute("SHOW transaction_read_only").fetchone()[0] == "on"
        return read(source, pending)

    monkeypatch.setattr(module, "_read_candidates", checked)
    query = tmp_path / "canonical.sql"
    query.write_text(
        "SELECT kind,source_id,part,lang,current_setting('transaction_read_only') AS title,text FROM source_documents;"
    )
    assert (
        main(
            [
                "--source-dsn",
                databases[0],
                "--canonical-dsn",
                databases[1],
                "--target-dsn",
                databases[2],
                "--canonical-query-file",
                str(query),
                "--group-size",
                "1",
                "--offset",
                "1",
                "--limit",
                "1",
            ]
        )
        == 0
    )
    result = json.loads(capsys.readouterr().out)
    assert result["documents"] == 1 and result["seeded"] == 1
    with psycopg.connect(databases[2]) as target:
        assert target.execute("SELECT source_id,title FROM semantic_chunks WHERE kind='ruling'").fetchall() == [
            (1, "on")
        ]


@pytest.mark.parametrize("which", [0, 2])
def test_seed_refuses_incompatible_source_or_target_storage(databases, which):
    document(databases, 1, "source")
    with psycopg.connect(databases[which]) as conn:
        conn.execute("ALTER TABLE semantic_chunks ALTER COLUMN embedding TYPE vector(768) USING embedding::vector(768)")
    with pytest.raises(SeedError, match="halfvec"):
        seed(*databases)
    with psycopg.connect(databases[2]) as target:
        assert target.execute("SELECT count(*) FROM semantic_chunks WHERE kind='ruling'").fetchone()[0] == 0


def test_seed_uses_existing_private_model_advisory_lock(databases):
    import time
    from concurrent.futures import ThreadPoolExecutor

    document(databases, 1, "source")
    with psycopg.connect(databases[2], autocommit=True) as owner, ThreadPoolExecutor(max_workers=1) as executor:
        owner.execute("SELECT pg_advisory_lock(hashtextextended('lebleg-semantic:' || %s,0))", (MODEL,))
        future = executor.submit(seed, *databases)
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                waiting = owner.execute(
                    "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event='advisory')"
                ).fetchone()[0]
                if waiting:
                    break
                time.sleep(0.01)
            else:
                pytest.fail("seed did not wait on the private index model lock")
            assert not future.done()
            assert owner.execute("SELECT count(*) FROM semantic_chunks WHERE kind='ruling'").fetchone()[0] == 0
        finally:
            owner.execute("SELECT pg_advisory_unlock(hashtextextended('lebleg-semantic:' || %s,0))", (MODEL,))
        assert future.result(timeout=10)["seeded"] == 1


def test_seed_cli_never_prints_connection_secret(capsys):
    assert (
        main(
            [
                "--source-dsn",
                "postgresql://owner:SECRET@127.0.0.1:1/nope",
                "--canonical-dsn",
                "unused",
                "--target-dsn",
                "unused",
            ]
        )
        == 1
    )
    output = capsys.readouterr()
    assert "SECRET" not in output.out + output.err


def test_seed_canonical_function_cannot_write(databases):
    document(databases, 1, "source")
    with psycopg.connect(databases[1]) as canonical:
        canonical.execute(
            "CREATE FUNCTION mutate() RETURNS text LANGUAGE plpgsql AS $$BEGIN UPDATE source_documents SET text='modified';RETURN 'modified';END$$"
        )
    query = "SELECT kind,source_id,part,lang,title,mutate() AS text FROM source_documents"
    with pytest.raises(SeedError):
        seed(*databases, canonical_query=query)
    with psycopg.connect(databases[1]) as canonical:
        assert canonical.execute("SELECT text FROM source_documents").fetchone()[0] == "source"


@pytest.mark.parametrize("null_vector", [False, True])
def test_seed_repairs_invalid_target_vector_then_leaves_valid_row_unchanged(databases, null_vector):
    document(databases, 1, "canonical source")
    assert seed(*databases)["seeded"] == 1
    with psycopg.connect(databases[2]) as target:
        if null_vector:
            target.execute("ALTER TABLE semantic_chunks ALTER COLUMN embedding DROP NOT NULL")
            target.execute("UPDATE semantic_chunks SET embedding=NULL,updated_at='2020-01-01' WHERE kind='ruling'")
        else:
            target.execute(
                "UPDATE semantic_chunks SET embedding=%s::halfvec,updated_at='2020-01-01' WHERE kind='ruling'",
                ("[" + ",".join(["0"] * 768) + "]",),
            )
    repaired = seed(*databases)
    assert repaired["seeded"] == 1 and repaired["skipped"] == 0
    with psycopg.connect(databases[2]) as target:
        before = target.execute(
            "SELECT embedding::text,updated_at,ctid::text FROM semantic_chunks WHERE kind='ruling'"
        ).fetchone()
        assert before[0] == vector()
    repeated = seed(*databases)
    assert repeated["seeded"] == 0 and repeated["skipped"] == 1
    with psycopg.connect(databases[2]) as target:
        assert (
            target.execute(
                "SELECT embedding::text,updated_at,ctid::text FROM semantic_chunks WHERE kind='ruling'"
            ).fetchone()
            == before
        )
