"""Retrieval policy tests with a deterministic database boundary and no network."""

import hashlib
import json
from contextlib import contextmanager

import pytest
from test_runtime import manifest

from lebleg_starter.embedding import ProviderError
from lebleg_starter.search import SearchService


def source_row(source_id, text):
    digest = hashlib.sha256(text.encode()).hexdigest()
    return {
        "kind": "law",
        "source_id": source_id,
        "part": "body",
        "lang": "ar",
        "title": text,
        "text": text,
        "hash": digest,
        "source_hash": digest,
        "source_text": text,
        "source_url": "https://example.org/source",
        "chunk_start": 0,
        "chunk_end": len(text),
        "metadata": {},
    }


class Result:
    def __init__(self, rows):
        self.rows = rows

    def fetchone(self):
        return self.rows[0] if self.rows else None

    def fetchall(self):
        return self.rows


def service_with_rows(provider, exact=None, semantic=None):
    class Connection:
        def execute(self, query, params=None):
            if "SELECT r.manifest" in query:
                return Result([{"manifest": manifest()}])
            if "FROM source_documents WHERE" in query:
                return Result([] if "AND NOT (title ILIKE" in query else exact or [])
            if "FROM semantic_chunks" in query:
                return Result(semantic or [])
            pytest.fail("unexpected database statement")

    @contextmanager
    def connection():
        yield Connection()

    service = SearchService.__new__(SearchService)
    service.provider = provider
    service.connection = connection
    return service


class FailingProvider:
    model = "gemini-embedding-001"

    def embed(self, *args, **kwargs):
        raise ProviderError("SECRET-key provider failure")

    def select(self, *args, **kwargs):
        raise ProviderError("SECRET-key selector failure")


def test_embedding_failure_keeps_ordinary_passages_and_safe_status():
    service = service_with_rows(FailingProvider(), exact=[source_row(1, "original source")])
    result = service.search("original")
    assert [p["text"] for p in result["passages"]] == ["original source"]
    assert result["semantic_status"] == "unavailable"
    assert result["evidence_status"] == "passages_found"
    assert "SECRET" not in json.dumps(result)


def test_failed_embedding_without_ordinary_results_signals_insufficient_evidence():
    result = service_with_rows(FailingProvider()).search("unmatched")
    assert result["passages"] == []
    assert result["evidence_status"] == "insufficient_evidence"
    assert result["semantic_status"] == "unavailable"


def test_selection_receives_only_semantic_preserves_exact_order():
    class Provider:
        model = "gemini-embedding-001"

        def embed(self, *args, **kwargs):
            return [[1.0] * 768]

        def select(self, query, passages):
            assert [p["source_id"] for p in passages] == [3, 4]
            assert all(not p["exact"] for p in passages)
            return passages[-1:]

    service = service_with_rows(
        Provider(),
        exact=[source_row(1, "first exact"), source_row(2, "second exact")],
        semantic=[source_row(3, "first semantic"), source_row(4, "second semantic")],
    )
    result = service.search("exact", select=True)
    assert [p["source_id"] for p in result["passages"]] == [1, 2, 4]
    assert result["selection_status"] == "complete"


def test_selection_failure_keeps_exact_and_original_semantic_passages():
    class Provider(FailingProvider):
        def embed(self, *args, **kwargs):
            return [[1.0] * 768]

    service = service_with_rows(
        Provider(), exact=[source_row(1, "original exact")], semantic=[source_row(2, "original semantic")]
    )
    result = service.search("original", select=True)
    assert [p["source_id"] for p in result["passages"]] == [1, 2]
    assert result["selection_status"] == "unavailable"
    assert "SECRET" not in json.dumps(result)


def test_missing_provider_selection_keeps_ordinary_search():
    result = service_with_rows(None, exact=[source_row(1, "ordinary")]).search("ordinary", select=True)
    assert [p["text"] for p in result["passages"]] == ["ordinary"]
    assert result["selection_status"] == "unavailable"


def test_empty_semantic_selection_returns_fixed_insufficient_evidence():
    class Provider:
        model = "gemini-embedding-001"

        def embed(self, *args, **kwargs):
            return [[1.0] * 768]

        def select(self, *args, **kwargs):
            return []

    result = service_with_rows(Provider(), semantic=[source_row(1, "candidate")]).search("unmatched", select=True)
    assert result["passages"] == []
    assert result["evidence_status"] == "insufficient_evidence"


def test_selection_owns_text_and_links_even_if_provider_returns_rewritten_fields():
    class Provider:
        model = "gemini-embedding-001"

        def embed(self, *args, **kwargs):
            return [[1.0] * 768]

        def select(self, query, passages):
            return [{**passages[0], "text": "invented wording", "source_url": "https://invented.example"}]

    result = service_with_rows(Provider(), semantic=[source_row(1, "original source")]).search("unmatched", select=True)
    assert result["passages"][0]["text"] == "original source"
    assert result["passages"][0]["source_url"] == "https://example.org/source"


def test_exact_only_selection_never_calls_provider():
    class NoCalls:
        model = "gemini-embedding-001"

        def embed(self, *args, **kwargs):
            pytest.fail("typed citation reached embedding provider")

        def select(self, *args, **kwargs):
            pytest.fail("ordinary passages reached selector")

    result = service_with_rows(NoCalls(), exact=[source_row(1, "original")]).search("law 123", select=True)
    assert result["passages"][0]["text"] == "original"
    assert result["selection_status"] == "not_needed"
