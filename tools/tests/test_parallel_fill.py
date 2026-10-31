"""Bounded parallel provider calls with real PostgreSQL checkpoints."""

import threading
import time
from concurrent.futures import ThreadPoolExecutor as RealExecutor

import pytest
from test_build import db as database_fixture

db = database_fixture

from lebleg.build import coverage, ensure_semantic, fill_missing, main
from lebleg.chunks import digest


def documents(conn, count):
    ensure_semantic(conn)
    conn.execute(
        "CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text)"
    )
    for index in range(count):
        text = "نص " + str(index)
        conn.execute("INSERT INTO source_documents VALUES('law',%s,'','ar','title',%s,%s)", (index, text, digest(text)))
    conn.commit()


class MainThreadConnection:
    def __init__(self, conn):
        self.conn = conn
        self.owner = threading.get_ident()

    def __getattr__(self, name):
        value = getattr(self.conn, name)
        if not callable(value):
            return value

        def call(*args, **kwargs):
            assert threading.get_ident() == self.owner, "database use escaped writer thread"
            return value(*args, **kwargs)

        return call


def test_parallel_fill_bounds_futures_and_database_writer(monkeypatch, db):
    import lebleg.build as module

    documents(db, 6)
    pending = set()
    lock = threading.Lock()
    peak = [0]

    class BoundedExecutor(RealExecutor):
        def submit(self, *args, **kwargs):
            with lock:
                pending.difference_update([future for future in pending if future.done()])
                assert len(pending) < 3, "unbounded queued provider futures"
                future = super().submit(*args, **kwargs)
                pending.add(future)
                peak[0] = max(peak[0], len(pending))

            def done(future):
                with lock:
                    pending.discard(future)

            future.add_done_callback(done)
            return future

    monkeypatch.setattr(module, "ThreadPoolExecutor", BoundedExecutor)
    barrier = threading.Barrier(3)
    provider_threads = set()

    class Provider:
        def embed(self, texts, query=False):
            assert len(texts) == 1
            provider_threads.add(threading.get_ident())
            barrier.wait(timeout=10)
            time.sleep(0.02)
            return [[0.1] * 768]

    assert fill_missing(MainThreadConnection(db), Provider(), cap=6, checkpoint=True, workers=3, batch_size=1) == 6
    assert peak[0] == 3
    assert len(provider_threads) == 3
    assert coverage(db) == {"expected": 6, "indexed": 6, "missing": 0}


def test_parallel_failure_checkpoints_all_successful_inflight_batches_and_stops(db):
    documents(db, 8)
    started = threading.Barrier(3)
    lock = threading.Lock()
    calls = []

    class Provider:
        def embed(self, texts, query=False):
            with lock:
                index = len(calls)
                calls.append(texts)
            started.wait(timeout=10)
            if index == 1:
                raise RuntimeError("temporary provider failure")
            time.sleep(0.05)
            return [[0.1] * 768]

    with pytest.raises(RuntimeError, match="temporary"):
        fill_missing(MainThreadConnection(db), Provider(), cap=8, checkpoint=True, workers=3, batch_size=1)
    db.rollback()
    assert len(calls) == 3
    assert coverage(db) == {"expected": 8, "indexed": 2, "missing": 6}

    class Resume:
        calls = 0

        def embed(self, texts, query=False):
            self.calls += len(texts)
            return [[0.1] * 768 for _ in texts]

    resume = Resume()
    assert fill_missing(db, resume, cap=6, checkpoint=True, workers=2, batch_size=2) == 6
    assert resume.calls == 6
    assert coverage(db)["missing"] == 0


def test_parallel_cap_fails_before_any_provider_submission(db):
    documents(db, 3)

    class NoCalls:
        def embed(self, *args, **kwargs):
            pytest.fail("cap exceeded before provider call")

    with pytest.raises(ValueError, match="cap"):
        fill_missing(db, NoCalls(), cap=2, workers=3, batch_size=1)
    assert db.execute("SELECT count(*) FROM semantic_chunks").fetchone()[0] == 0


@pytest.mark.parametrize(
    "flag,value",
    [
        ("--embedding-workers", "0"),
        ("--embedding-workers", "9"),
        ("--embedding-batch-size", "0"),
        ("--embedding-batch-size", "101"),
    ],
)
def test_parallel_cli_rejects_invalid_bounds(flag, value, tmp_path):
    with pytest.raises(SystemExit) as error:
        main(
            [
                "--database-url",
                "unused",
                "--snapshot-dir",
                "unused",
                "--revision",
                "a" * 40,
                "--release-id",
                "test",
                "--output-dir",
                str(tmp_path),
                flag,
                value,
            ]
        )
    assert error.value.code == 2


@pytest.mark.parametrize("custom_ledger", [False, True])
def test_cli_token_budget_wraps_provider_before_paid_request(monkeypatch, db, tmp_path, custom_ledger):
    import os

    from psycopg.conninfo import make_conninfo

    import lebleg.embedding as module
    from lebleg.budget import BudgetError

    documents(db, 1)
    db.execute("CREATE TABLE lebleg_build_state(release_id text,revision text,tables jsonb,as_of_date date)")
    db.execute("INSERT INTO lebleg_build_state VALUES('parallel-test',%s,'{}','2026-10-30')", ("a" * 40,))
    db.commit()

    class Provider:
        model = "gemini-embedding-001"

        def __init__(self, key):
            assert key == "synthetic"

        def count_tokens(self, texts):
            return 10

        def embed(self, *args, **kwargs):
            pytest.fail("token budget exceeded before paid request")

    monkeypatch.setattr(module, "GeminiEmbedder", Provider)
    monkeypatch.setenv("GEMINI_API_KEY", "synthetic")
    output = tmp_path / "output"
    ledger = tmp_path / "custom.json" if custom_ledger else output / "embedding-budget.json"
    argv = [
        "--database-url",
        make_conninfo(os.environ["LEBLEG_TEST_ADMIN_DSN"], dbname=db.info.dbname),
        "--snapshot-dir",
        "unused",
        "--revision",
        "a" * 40,
        "--release-id",
        "parallel-test",
        "--output-dir",
        str(output),
        "--as-of-date",
        "2026-10-30",
        "--resume",
        "--embed-missing",
        "--embedding-workers",
        "2",
        "--embedding-batch-size",
        "1",
        "--max-input-tokens",
        "1",
    ]
    if custom_ledger:
        argv += ["--embedding-budget-ledger", str(ledger)]
    with pytest.raises(BudgetError, match="exhausted"):
        main(argv)
    assert ledger.with_name(ledger.name + ".lock").exists()
    assert not (output / "corpus.dump").exists()


def test_failure_while_preparing_next_batch_prevents_late_submission(monkeypatch, db):
    import lebleg.build as module

    documents(db, 3)
    reading_second = threading.Event()
    submitted = []
    calls = []

    class Executor(RealExecutor):
        def submit(self, *args, **kwargs):
            future = super().submit(*args, **kwargs)
            submitted.append(future)
            return future

    original = module.missing_chunks

    def interrupted_producer(conn, checkpoint=False):
        canonical = original(conn, checkpoint=checkpoint)
        try:
            yield next(canonical)
            reading_second.set()
            assert isinstance(submitted[0].exception(timeout=10), RuntimeError)
            yield from canonical
        finally:
            canonical.close()

    class Provider:
        def embed(self, texts, query=False):
            calls.append(texts)
            if len(calls) == 1:
                assert reading_second.wait(timeout=10)
                raise RuntimeError("provider stopped during preparation")
            return [[0.1] * 768]

    monkeypatch.setattr(module, "ThreadPoolExecutor", Executor)
    monkeypatch.setattr(module, "missing_chunks", interrupted_producer)
    with pytest.raises(RuntimeError, match="during preparation"):
        fill_missing(db, Provider(), cap=3, workers=2, batch_size=1, checkpoint=True)
    assert len(calls) == 1
