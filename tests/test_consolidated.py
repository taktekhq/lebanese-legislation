import json

from fastapi.testclient import TestClient

from lebleg_starter.server import create_app


class Search:
    def __init__(self):
        self.calls = []

    def search(self, query, limit=8, semantic=True, select=False, **filters):
        self.calls.append((query, limit, semantic, select, filters))
        return {
            "passages": [
                {
                    "kind": "law",
                    "source_id": 1,
                    "part": "title",
                    "lang": "ar",
                    "title": "عنوان",
                    "text": "النص الأصلي",
                    "exact": True,
                }
            ],
            "exact_counts": {"law": 1},
            "semantic_status": "not_needed",
        }

    def health(self):
        return True

    def close(self):
        pass


class General:
    def summary(self, source_id):
        return {"id": source_id, "title": "عنوان", "url": "https://example.org/law/1/"}

    def law(self, source_id):
        return {"id": source_id, "articles": [{"number": "1"}]}

    def gazette_search(self, query, **kwargs):
        return {"gazette_count": 0}


def rpc(client, method, params=None, request_id=1):
    return client.post("/mcp", json={"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}})


def test_http_mcp_share_engine_and_preserve_v1_shape():
    search = Search()
    with TestClient(create_app(service=search, general_service=General())) as client:
        http = client.get("/api/v1/search/", params={"q": "قانون", "type": "laws", "semantic": True}).json()
        response = rpc(
            client, "tools/call", {"name": "search_legislation", "arguments": {"query": "قانون", "kind": "laws"}}
        )
        assert response.status_code == 200
        tool = json.loads(response.json()["result"]["content"][0]["text"])["result"]
        assert tool == http
        assert http["laws"][0]["id"] == 1 and http["law_count"] == 1
        assert http["passages"][0]["text"] == "النص الأصلي"
        assert search.calls[0] == search.calls[1]
        assert client.get("/api/search", params={"q": "قانون"}).status_code == 200


def test_mcp_only_mode_and_protocol_errors_do_not_leak():
    with TestClient(create_app(service=Search(), general_service=General(), mode="mcp")) as client:
        for path in ["/search", "/api/v1/", "/docs", "/openapi.json"]:
            assert client.get(path).status_code == 404
        assert client.get("/healthz").status_code == 200
        assert client.get("/mcp").status_code == 405
        assert (
            rpc(client, "initialize", {"protocolVersion": "2025-03-26"}).json()["result"]["protocolVersion"]
            == "2025-03-26"
        )
        assert rpc(client, "tools/list").json()["result"]["tools"][0]["name"] == "search_legislation"
        assert (
            rpc(client, "tools/call", {"name": "get_law", "arguments": {"id": 1}}).json()["result"]["isError"] is False
        )
        assert (
            rpc(client, "tools/call", {"name": "get_law", "arguments": {"id": True}}).json()["error"]["code"] == -32602
        )
        assert client.post("/mcp", content="[1]").status_code == 415
        assert client.post("/mcp", content="{", headers={"content-type": "application/json"}).status_code == 400
        assert client.post("/mcp", json={"jsonrpc": "2.0", "method": "notifications/initialized"}).status_code == 202
        assert client.post("/mcp", json={}, headers={"Origin": "https://untrusted.example"}).status_code == 403
        assert (
            client.post(
                "/mcp", json={}, headers={"Origin": "http://attacker.example", "Host": "attacker.example"}
            ).status_code
            == 403
        )
        assert client.post("/mcp", json={}, headers={"MCP-Protocol-Version": "unsupported"}).status_code == 400
        assert client.post("/mcp", content="x" * 65537, headers={"content-type": "application/json"}).status_code == 413
