"""The four services in Compose on a tiny generated release, never public data."""

import hashlib
import json
import os
import subprocess
import uuid
from pathlib import Path

import httpx
import psycopg
import pytest
from corpus_fixture import (
    build_corpus,  # noqa: F401
    corpus,  # noqa: F401
)
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict
from psycopg.types.json import Jsonb

ROOT = Path(__file__).parents[2]


@pytest.fixture
def service_images():
    names = {service: os.getenv(f"LEBLEG_TEST_{service.upper()}_IMAGE") for service in ("db", "api", "mcp", "web")}
    if not all(names.values()) or not os.getenv("PG_DUMP_BIN"):
        pytest.skip("set LEBLEG_TEST_DB_IMAGE/API_IMAGE/MCP_IMAGE/WEB_IMAGE and PG_DUMP_BIN for isolated Compose integration")
    return names


def synthetic_release(dsn, directory):
    with psycopg.connect(dsn) as conn:
        manifest = conn.execute("SELECT manifest FROM lebleg_release").fetchone()[0]
        conn.execute("CREATE INDEX fixture_hnsw ON semantic_chunks USING hnsw(embedding halfvec_cosine_ops)")
        tables = conn.execute(
            "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename <> 'lebleg_release'"
        ).fetchall()
        counts = {
            name: conn.execute(sql.SQL("SELECT count(*) FROM {}").format(sql.Identifier(name))).fetchone()[0]
            for (name,) in tables
        }
        manifest["counts"] = {
            "tables": counts, "source_documents": counts["source_documents"], "semantic_chunks": counts["semantic_chunks"]
        }
        conn.execute("UPDATE lebleg_release SET manifest=%s", (Jsonb(manifest),))
    directory.chmod(0o755)
    dump = directory / manifest["dump"]["path"]
    environment = os.environ.copy()
    environment.update({
        {"dbname": "PGDATABASE", "password": "PGPASSWORD"}.get(key, "PG" + key.upper()): value
        for key, value in conninfo_to_dict(dsn).items()
    })
    with dump.open("wb") as output:
        subprocess.run(
            [os.environ["PG_DUMP_BIN"], "--format=custom", "--no-owner", "--no-acl"],
            env=environment, stdout=output, stderr=subprocess.PIPE, check=True, timeout=60,
        )
    manifest["dump"]["size_bytes"] = dump.stat().st_size
    manifest["dump"]["sha256"] = hashlib.sha256(dump.read_bytes()).hexdigest()
    path = directory / "manifest.json"
    path.write_text(json.dumps(manifest), encoding="utf-8")
    dump.chmod(0o644)
    path.chmod(0o644)
    return manifest


def test_services_restore_and_share_results(service_images, corpus, tmp_path):  # noqa: F811
    manifest = synthetic_release(corpus, tmp_path)
    assert manifest["counts"]["source_documents"] == manifest["counts"]["semantic_chunks"] == 4
    assert manifest["counts"]["tables"]["legislation_article"] > 600
    project = "lebleg-compose-test-" + uuid.uuid4().hex[:12]
    environment = {**os.environ,
        "COMPOSE_PROJECT_NAME": project, "POSTGRES_PASSWORD": "compose-fixture-owner",
        "READER_PASSWORD": "compose-fixture-reader", "LEBLEG_RELEASE_DIR": str(tmp_path),
        "DB_IMAGE": service_images["db"], "API_IMAGE": service_images["api"], "MCP_IMAGE": service_images["mcp"],
        "WEB_IMAGE": service_images["web"],
        "DB_PORT": "0", "API_PORT": "0", "MCP_PORT": "0", "WEB_PORT": "0", "GEMINI_API_KEY": ""
    }
    command = ["docker", "compose", "--project-name", project, "--env-file", "/dev/null",
               "-f", str(ROOT / "compose.yaml"), "-f", str(ROOT / "compose.local.yaml")]

    def compose(*arguments, check=True, timeout=60):
        return subprocess.run(command + list(arguments), env=environment, cwd=ROOT,
                              capture_output=True, text=True, check=check, timeout=timeout)

    def address(service, port="8000"):
        return "http://" + compose("port", service, port).stdout.strip()

    try:
        started = compose("up", "-d", "--pull", "never", "--wait", "--wait-timeout", "180", check=False, timeout=210)
        assert started.returncode == 0, started.stderr + compose("logs", "--no-color", check=False).stdout[-10000:]
        state_json = compose("ps", "--format", "json").stdout
        try:
            states = json.loads(state_json)
        except json.JSONDecodeError:
            states = [json.loads(line) for line in state_json.splitlines()]
        if isinstance(states, dict):
            states = [states]
        assert {state["Service"] for state in states} == {"db", "api", "mcp", "web"}
        assert all(state["Health"] == "healthy" for state in states)
        api, mcp, web = address("api"), address("mcp", "8080"), address("web", "8080")
        with httpx.Client(timeout=10) as client:
            law = client.get(api + "/api/v1/law/1/")
            assert law.status_code == 200, law.text
            assert len(law.json()["articles"]) > 600
            article = client.get(api + "/api/v1/law/1/article/1/")
            assert article.status_code == 200, article.text
            assert article.json()["text_ar"] == "نص تعديل أصلي"
            assert "calculated_current" not in article.json()
            result = client.get(api + "/api/v1/search/", params={"q": "نص", "type": "laws"})
            assert result.status_code == 200, result.text
            assert result.json()["law_count"] == 2
            # Without GEMINI_API_KEY semantic search says so instead of failing.
            semantic = client.get(api + "/api/v1/search/", params={"q": "نص", "type": "laws", "semantic": "true"})
            assert semantic.json()["semantic"] == "unavailable"
            response = client.post(mcp + "/mcp", json={
                "jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
                    "name": "search_legislation", "arguments": {"query": "نص", "kind": "laws"}
                },
            })
            assert response.status_code == 200, response.text
            tool = response.json()
            assert "error" not in tool, tool
            assert json.loads(tool["result"]["content"][0]["text"])["result"] == result.json()
            assert client.get(mcp + "/api/v1/law/1/").status_code == 404
            assert client.get(mcp + "/search/", params={"q": "نص"}).status_code == 404
            assert client.get(api + "/mcp").status_code == 404
            assert client.get(api + "/law/1/").status_code == 404
            # The website reads the same restored corpus, keyword search included (db/web.sql).
            page = client.get(web + "/law/1/")
            assert page.status_code == 200, page.text[:500]
            assert "نص فرعي" in page.text
            found = client.get(web + "/search/", params={"q": "نص"})
            assert found.status_code == 200, found.text[:500]
            assert "/law/1/" in found.text
    finally:
        compose("down", "--volumes", "--remove-orphans", "--timeout", "5", check=False, timeout=45)
