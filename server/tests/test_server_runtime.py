from fastapi.testclient import TestClient

from lebleg.server import create_app


class Service:
    def health(self):
        return True

    def search(self, query, limit=8, semantic=True, select=False):
        if query == "failure":
            raise RuntimeError("postgresql://owner:SECRET@db")
        return {"passages": [{"text": "original"}]}

    def close(self):
        pass


def test_api_validation_and_safe_failures():
    with TestClient(create_app(service=Service())) as client:
        assert client.get("/healthz").json() == {"ready": True}
        assert client.get("/api/search", params={"q": "source"}).json()["passages"][0]["text"] == "original"
        assert client.get("/api/search", params={"q": "x" * 501}).status_code == 422
        result = client.get("/api/search", params={"q": "failure"})
        assert result.status_code == 503
        assert "SECRET" not in result.text
