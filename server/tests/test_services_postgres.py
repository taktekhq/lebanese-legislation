"""HTTP/MCP parity through real source tables and saved vectors."""

import hashlib
import json

import psycopg
from fastapi.testclient import TestClient
from psycopg.types.json import Jsonb
from test_general_postgres import general  # noqa: F401
from test_runtime import manifest

from lebleg.server import create_app


class Provider:
    model = "gemini-embedding-001"

    def __init__(self):
        self.calls = 0

    def embed(self, *args, **kwargs):
        self.calls += 1
        return [[1.0] * 768]


def prepare_service_source(general_service):
    service = general_service.search_service
    provider = Provider()
    service.provider = provider
    with psycopg.connect(service.pool.conninfo) as conn:
        conn.execute("CREATE EXTENSION vector")
        conn.execute(
            "CREATE TABLE source_documents(kind text,source_id int,part text,lang text,title text,text text,hash text,source_url text,metadata jsonb,PRIMARY KEY(kind,source_id,part,lang))"
        )
        conn.execute(
            "CREATE TABLE semantic_chunks(model text,kind text,source_id int,part text,lang text,title text,text text,hash text,chunk_start int,chunk_end int,embedding halfvec(768))"
        )
        conn.execute("CREATE TABLE lebleg_release(release_id text,manifest jsonb)")
        conn.execute("CREATE TABLE restore_receipt(singleton boolean,manifest jsonb)")
        conn.execute(
            "INSERT INTO legislation_article(law_id,position,number,text_ar) VALUES(1,602,'12 مكرر/ب','نص فرعي')"
        )
        m = manifest()
        conn.execute("INSERT INTO lebleg_release VALUES(%s,%s)", (m["release_id"], Jsonb(m)))
        conn.execute("INSERT INTO restore_receipt VALUES(true,%s)", (Jsonb(m),))
        for kind, sid, part, text, year in [
            ("law", 1, "title", "نص أصلي", 2019),
            ("law", 1, "preamble", "مقدمة أصلية", 2019),
            ("law", 2, "title", "نص ثان", 2020),
            ("article", 1, "1", "نص تعديل أصلي", 2019),
        ]:
            title = "نص أصلي" if sid == 1 else "نص ثان"
            digest = hashlib.sha256(text.encode()).hexdigest()
            conn.execute(
                "INSERT INTO source_documents VALUES(%s,%s,%s,'ar',%s,%s,%s,'https://example.org/source',%s)",
                (kind, sid, part, title, text, digest, Jsonb({"issued_on": f"{year}-01-01", "source_type": "law"})),
            )
            conn.execute(
                "INSERT INTO semantic_chunks VALUES(%s,%s,%s,%s,'ar',%s,%s,%s,0,%s,%s::halfvec)",
                (provider.model, kind, sid, part, title, text, digest, len(text), "[" + ",".join(["1"] * 768) + "]"),
            )
    return service, provider, m


def test_real_shared_retrieval_filters_pagination_and_source_integrity(general):  # noqa: F811
    service, provider, _manifest = prepare_service_source(general)
    with TestClient(create_app(service=service, general_service=general)) as client:
        first = client.get("/api/v1/search/", params={"q": "نص", "type": "laws", "limit": 1}).json()
        second = client.get("/api/v1/search/", params={"q": "نص", "type": "laws", "limit": 1, "offset": 1}).json()
        assert first["law_count"] == second["law_count"] == 2
        assert first["laws"][0]["id"] == 1 and second["laws"][0]["id"] == 2
        assert (
            client.get("/api/v1/search/", params={"q": "نص", "type": "laws", "year": 2020}).json()["laws"][0]["id"] == 2
        )
        assert client.get("/api/v1/law/1/").json()["article_count"] == 601
        assert client.get("/api/v1/law/1/article/12-%D9%85%D9%83%D8%B1%D8%B1%2F%D8%A8/").json()["text_ar"] == "نص فرعي"
        assert client.get("/api/v1/law/9999/").status_code == 404
        arguments = {"query": "unmatched concept", "kind": "laws", "year": 2020, "semantic": True}
        http = client.get(
            "/api/v1/search/", params={"q": arguments["query"], "type": "laws", "year": 2020, "semantic": True}
        ).json()
        tool = client.post(
            "/mcp",
            json={
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {"name": "search_legislation", "arguments": arguments},
            },
        ).json()
        assert json.loads(tool["result"]["content"][0]["text"])["result"] == http
        assert http["semantic_status"] == "complete"
        assert {p["source_id"] for p in http["passages"]} == {2}
        assert http["passages"][0]["text"] == "نص ثان"
        calls = provider.calls
        missing = client.get("/search", params={"q": "law 999999"}).json()
        assert missing["passages"] == [] and provider.calls == calls
