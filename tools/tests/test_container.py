"""Optional real image check, including dynamic shared-memory HNSW construction."""

import os
import subprocess
import time
import uuid

import psycopg
import pytest


def test_public_postgres_image_and_reader():
    image = os.getenv("LEBLEG_TEST_IMAGE")
    if not image:
        pytest.skip("set LEBLEG_TEST_IMAGE for exact-image integration")
    name = "lebleg-image-test-" + uuid.uuid4().hex[:10]
    try:
        subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "-d",
                "--name",
                name,
                "--memory",
                "2600m",
                "--shm-size",
                "1g",
                "-p",
                "127.0.0.1::5432",
                "-e",
                "POSTGRES_USER=owner",
                "-e",
                "POSTGRES_DB=test",
                "-e",
                "POSTGRES_PASSWORD=image-test-owner",
                "-e",
                "LEBLEG_READER_PASSWORD=image-test-reader",
                image,
                "postgres",
                "-c",
                "shared_buffers=1GB",
                "-c",
                "maintenance_work_mem=512MB",
                "-c",
                "max_parallel_maintenance_workers=1",
            ],
            check=True,
            capture_output=True,
        )
        port = subprocess.check_output(["docker", "port", name, "5432"], text=True).strip().rsplit(":", 1)[1]
        dsn = f"host=127.0.0.1 port={port} user=owner password=image-test-owner dbname=test"
        for _ in range(60):
            try:
                with psycopg.connect(dsn, autocommit=True) as conn:
                    conn.execute("SELECT 1")
                break
            except psycopg.OperationalError:
                time.sleep(0.5)
        else:
            pytest.fail("image did not become ready")
        with psycopg.connect(dsn, autocommit=True) as conn:
            assert conn.execute("SHOW server_version").fetchone()[0].startswith("18.6")
            conn.execute("CREATE EXTENSION vector")
            assert conn.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone()[0] == "0.8.2"
            conn.execute("CREATE TABLE image_probe(v halfvec(768)) WITH(parallel_workers=1)")
            conn.execute(
                "INSERT INTO image_probe SELECT array_fill(0.03::real,ARRAY[768])::halfvec FROM generate_series(1,20000)"
            )
            conn.execute("CREATE INDEX image_probe_hnsw ON image_probe USING hnsw(v halfvec_cosine_ops)")
            conn.execute("GRANT SELECT ON image_probe TO lebleg_reader")
        with psycopg.connect(
            f"host=127.0.0.1 port={port} user=lebleg_reader password=image-test-reader dbname=test", autocommit=True
        ) as conn:
            assert conn.execute("SHOW default_transaction_read_only").fetchone()[0] == "on"
            assert conn.execute("SELECT count(*) FROM image_probe").fetchone()[0] == 20000
            with pytest.raises((psycopg.errors.ReadOnlySqlTransaction, psycopg.errors.InsufficientPrivilege)):
                conn.execute("INSERT INTO image_probe SELECT array_fill(0.03::real,ARRAY[768])::halfvec")
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True, check=False)
