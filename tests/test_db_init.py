"""Synthetic first-boot restore checks. No public snapshot is downloaded."""

import importlib.util
import json
import os
import subprocess
import time
import uuid
from pathlib import Path

import pytest
from test_runtime import manifest
from test_runtime_postgres import databases as db_fixture  # noqa: F401
from test_runtime_postgres import prepare_dump


@pytest.fixture
def db_script():
    spec = importlib.util.spec_from_file_location("db_release", Path(__file__).parents[1] / "docker/db-release.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_local_manifest_uses_verified_restorer(db_script, monkeypatch, tmp_path):
    m = manifest()
    path = tmp_path / "manifest.json"
    path.write_text(json.dumps(m))
    monkeypatch.setenv("LEBLEG_MANIFEST_FILE", str(path))
    calls = []
    monkeypatch.setattr(db_script, "restore", lambda *args: calls.append(args) or "restored")
    db_script.initialize()
    assert calls[0][0] == m
    assert calls[0][1] == str(tmp_path / "snapshot.dump")
    assert "host=/var/run/postgresql" in calls[0][2]


def test_missing_manifest_fails_before_restore(db_script, monkeypatch, tmp_path):
    monkeypatch.setenv("LEBLEG_MANIFEST_FILE", str(tmp_path / "missing.json"))
    monkeypatch.setattr(db_script, "restore", lambda *args: pytest.fail("must not restore missing release"))
    with pytest.raises(OSError):
        db_script.initialize()


def test_health_refuses_socket_only_init_server(db_script, monkeypatch):
    def not_ready(command, **kwargs):
        assert command[:3] == ["pg_isready", "-h", "127.0.0.1"]
        raise subprocess.CalledProcessError(2, command)

    monkeypatch.setattr(db_script.subprocess, "run", not_ready)
    monkeypatch.setattr(db_script.psycopg, "connect", lambda *args, **kwargs: pytest.fail("TCP must precede receipt"))
    with pytest.raises(subprocess.CalledProcessError):
        db_script.healthy()


def docker(*args, check=True):
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check, timeout=90)


def wait_container(name, *, healthy):
    for _ in range(120):
        result = docker(
            "exec", name, "/opt/venv/bin/python", "/opt/starter/docker/db-release.py", "health", check=False
        )
        if healthy and result.returncode == 0:
            return
        state = docker("inspect", "--format", "{{.State.Status}}", name).stdout.strip()
        if not healthy and state == "exited":
            assert result.returncode != 0
            return
        time.sleep(0.5)
    logs = docker("logs", name, check=False).stdout
    pytest.fail(f"container did not reach expected state: {logs[-3000:]}")


@pytest.mark.parametrize("valid,with_reader", [(True, True), (True, False), (False, True)])
def test_db_image_first_boot_and_restart(db_fixture, valid, with_reader):  # noqa: F811
    image = os.getenv("LEBLEG_TEST_DB_IMAGE")
    if not image:
        pytest.skip("set LEBLEG_TEST_DB_IMAGE for DB image first-boot checks")
    m, dump, _text = prepare_dump(db_fixture)
    if not valid:
        m["dump"]["sha256"] = "0" * 64
    directory = dump.parent
    # The shared fixture returns an explicit dump path; first-boot init resolves the manifest filename.
    dump = dump.rename(directory / m["dump"]["path"])
    directory.chmod(0o755)
    dump.chmod(0o644)
    (directory / "manifest.json").write_text(json.dumps(m))
    (directory / "manifest.json").chmod(0o644)
    pause = directory / "pause.sh"
    pause.write_text("#!/bin/sh\nwhile [ ! -e /tmp/lebleg-release-continue ]; do sleep 0.1; done\n")
    pause.chmod(0o755)
    name = "lebleg-init-test-" + uuid.uuid4().hex[:10]
    try:
        reader_env = ["-e", "LEBLEG_READER_PASSWORD=test-reader"] if with_reader else []
        docker(
            "run",
            "-d",
            "--name",
            name,
            "-v",
            f"{directory}:/release:ro",
            "-v",
            f"{pause}:/docker-entrypoint-initdb.d/40-pause.sh:ro",
            "-e",
            "POSTGRES_PASSWORD=test-owner",
            "-e",
            "POSTGRES_DB=fixture",
            *reader_env,
            "-e",
            "LEBLEG_MANIFEST_FILE=/release/manifest.json",
            image,
        )
        if valid:
            # Pause after verified restore, before the temporary socket-only
            # server stops. A valid receipt alone must not satisfy readiness.
            for _ in range(120):
                receipt = docker(
                    "exec",
                    name,
                    "psql",
                    "-U",
                    "postgres",
                    "-d",
                    "fixture",
                    "-Atc",
                    "SELECT count(*) FROM restore_receipt",
                    check=False,
                )
                if receipt.returncode == 0 and receipt.stdout.strip() == "1":
                    break
                time.sleep(0.5)
            else:
                pytest.fail("temporary init server never received its restore receipt")
            assert (
                docker(
                    "exec", name, "/opt/venv/bin/python", "/opt/starter/docker/db-release.py", "health", check=False
                ).returncode
                != 0
            )
            docker("exec", name, "touch", "/tmp/lebleg-release-continue")
        wait_container(name, healthy=valid)
        docker("restart", name)
        if valid:
            wait_container(name, healthy=True)
            count = docker(
                "exec", name, "psql", "-U", "postgres", "-d", "fixture", "-Atc", "SELECT count(*) FROM restore_receipt"
            ).stdout.strip()
            assert count == "1"
        else:
            # Official PostgreSQL skips init scripts on this initialized volume.
            # It may start, but a checksum failure must never acquire readiness.
            for _ in range(20):
                assert (
                    docker(
                        "exec", name, "/opt/venv/bin/python", "/opt/starter/docker/db-release.py", "health", check=False
                    ).returncode
                    != 0
                )
                time.sleep(0.1)
    finally:
        docker("rm", "-fv", name, check=False)


def test_postgres_standard_database_default(db_script, monkeypatch):
    from psycopg.conninfo import conninfo_to_dict

    monkeypatch.setenv("POSTGRES_USER", "custom_owner")
    monkeypatch.delenv("POSTGRES_DB", raising=False)
    monkeypatch.delenv("POSTGRES_PASSWORD_FILE", raising=False)
    assert conninfo_to_dict(db_script.database_url())["dbname"] == "custom_owner"
