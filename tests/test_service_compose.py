"""Three-service Compose contract using a tiny generated fixture, never public data."""

import hashlib
import json
import os
import subprocess
import uuid
from pathlib import Path

import httpx
import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict
from psycopg.types.json import Jsonb
from test_general_postgres import general  # noqa: F401
from test_services_postgres import prepare_service_source

ROOT = Path(__file__).parents[1]


@pytest.fixture
def service_images():
    names = {service: os.getenv(f"LEBLEG_TEST_{service.upper()}_IMAGE") for service in ("db", "api", "mcp")}
    if not all(names.values()) or not os.getenv("PG_DUMP_BIN"):
        pytest.skip("set LEBLEG_TEST_DB_IMAGE/API_IMAGE/MCP_IMAGE and PG_DUMP_BIN for isolated Compose integration")
    return names


def synthetic_release(general_service, directory):
    service, _provider, manifest = prepare_service_source(general_service)
    with psycopg.connect(service.pool.conninfo) as conn:
        conn.execute("DROP TABLE restore_receipt")
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
        for key, value in conninfo_to_dict(service.pool.conninfo).items()
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


def test_three_services_restore_and_share_results(service_images, general, tmp_path):  # noqa: F811
    manifest = synthetic_release(general, tmp_path)
    assert manifest["counts"]["source_documents"] == manifest["counts"]["semantic_chunks"] == 4
    assert manifest["counts"]["tables"]["legislation_article"] > 600
    project = "lebleg-compose-test-" + uuid.uuid4().hex[:12]
    environment = {**os.environ, 
        "COMPOSE_PROJECT_NAME": project, "POSTGRES_PASSWORD": "compose-fixture-owner",
        "READER_PASSWORD": "compose-fixture-reader", "LEBLEG_RELEASE_DIR": str(tmp_path),
        "DB_IMAGE": service_images["db"], "API_IMAGE": service_images["api"], "MCP_IMAGE": service_images["mcp"],
        "DB_PORT": "0", "API_PORT": "0", "MCP_PORT": "0", "GEMINI_API_KEY": ""
    }
    command = ["docker", "compose", "--project-name", project, "--env-file", "/dev/null",
               "-f", str(ROOT / "compose.yaml"), "-f", str(ROOT / "compose.local.yaml")]

    def compose(*arguments, check=True, timeout=60):
        return subprocess.run(command + list(arguments), env=environment, cwd=ROOT,
                              capture_output=True, text=True, check=check, timeout=timeout)

    def address(service):
        return "http://" + compose("port", service, "8000").stdout.strip()

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
        assert {state["Service"] for state in states} == {"db", "api", "mcp"}
        assert all(state["Health"] == "healthy" for state in states)
        api, mcp = address("api"), address("mcp")
        with httpx.Client(timeout=10) as client:
            law = client.get(api + "/api/v1/law/1/")
            assert law.status_code == 200, law.text
            assert law.json()["article_count"] > 600
            assert len(law.json()["articles"]) > 600
            article = client.get(api + "/api/v1/law/1/article/12-%D9%85%D9%83%D8%B1%D8%B1%2F%D8%A8/")
            assert article.status_code == 200, article.text
            assert article.json()["text_ar"] == "نص فرعي"
            result = client.get(api + "/api/v1/search/", params={"q": "نص", "type": "laws", "semantic": "false"})
            assert result.status_code == 200, result.text
            assert result.json()["law_count"] == 2
            response = client.post(mcp + "/mcp", json={
                "jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
                    "name": "search_legislation", "arguments": {"query": "نص", "kind": "laws", "semantic": False}
                },
            })
            assert response.status_code == 200, response.text
            tool = response.json()
            assert "error" not in tool, tool
            assert json.loads(tool["result"]["content"][0]["text"])["result"] == result.json()
            assert client.get(mcp + "/api/v1/law/1/").status_code == 404
            assert client.get(mcp + "/search", params={"q": "نص"}).status_code == 404
    finally:
        compose("down", "--volumes", "--remove-orphans", "--timeout", "5", check=False, timeout=45)
