import hashlib
import math

import pytest

from lebleg_starter.embedding import ProviderError, normalize, validate_selection
from lebleg_starter.manifest import ManifestError, validate_manifest, verify_file
from lebleg_starter.search import citation_target, validate_passage


def manifest():
    return {
        "format_version": 1,
        "release_id": "test-1",
        "generated_at": "2026-10-30T00:00:00Z",
        "source": {"repo_id": "taktekhq/lebanese-legislation", "revision": "a" * 40},
        "postgres": {"version": "18.6", "vector_version": "0.8.2"},
        "embedding": {
            "model": "gemini-embedding-001",
            "dimensions": 768,
            "task_type": "RETRIEVAL_DOCUMENT",
            "chunk_size": 900,
            "chunk_overlap": 120,
        },
        "counts": {
            "tables": {"source_documents": 1, "semantic_chunks": 1},
            "source_documents": 1,
            "semantic_chunks": 1,
        },
        "dataset": {"license": "existing", "attribution": "source"},
        "build_version": "0.1.0",
        "dump": {
            "path": "snapshot.dump",
            "size_bytes": 3,
            "sha256": hashlib.sha256(b"abc").hexdigest(),
        },
    }


@pytest.mark.parametrize("path", ["../x", "/tmp/x", "a/../x", "https://evil/x", "a\\x", "a//x"])
def test_reject_unsafe_artifact_path(path):
    m = manifest()
    m["dump"]["path"] = path
    with pytest.raises(ManifestError):
        validate_manifest(m)


def test_manifest_model_and_checksum(tmp_path):
    m = manifest()
    assert validate_manifest(m) == m
    p = tmp_path / "dump"
    p.write_bytes(b"abc")
    verify_file(p, m["dump"])
    p.write_bytes(b"abd")
    with pytest.raises(ManifestError):
        verify_file(p, m["dump"])
    m["embedding"]["dimensions"] = 3072
    with pytest.raises(ManifestError):
        validate_manifest(m)


@pytest.mark.parametrize("value", [[0.0] * 768, [math.nan] * 768, [1.0] * 767])
def test_embedding_rejects_invalid_vectors(value):
    with pytest.raises(ProviderError):
        normalize(value)


def test_selection_ids_only_and_owned_text():
    passages = [{"id": "p1", "text": "original"}]
    assert validate_selection({"source_ids": ["p1"]}, passages) == passages
    for value in [
        {"source_ids": ["invented"]},
        {"source_ids": ["p1", "p1"]},
        {"source_ids": ["p1"], "prose": "invented"},
    ]:
        with pytest.raises(ProviderError):
            validate_selection(value, passages)


def test_unicode_passage_rechecks_source_hash_and_offsets():
    source = "قانون 🙂 نص"
    h = hashlib.sha256(source.encode()).hexdigest()
    row = {"source_text": source, "source_hash": h, "hash": h, "chunk_start": 6, "chunk_end": 7, "text": "🙂"}
    assert validate_passage(row)
    row["source_text"] = "changed"
    assert not validate_passage(row)


def test_explicit_citation_recognized():
    assert citation_target("law 123 dated 2020-01-02").date == "2020-01-02"
    assert citation_target("قانون رقم ١٢٣").number == "123"
    assert citation_target("employment rights") is None


@pytest.mark.parametrize("revision", ["main", "a" * 39, "g" * 40])
def test_manifest_requires_immutable_input_revision(revision):
    m = manifest()
    m["source"]["revision"] = revision
    with pytest.raises(ManifestError):
        validate_manifest(m)


def test_provider_redirect_and_error_never_include_key():
    from urllib.error import HTTPError

    from lebleg_starter.embedding import Gemini

    provider = Gemini("SECRET-key")

    class Failed:
        def open(self, request, timeout):
            assert request.get_header("X-goog-api-key") == "SECRET-key"
            assert "SECRET-key" not in request.full_url
            raise HTTPError(request.full_url, 302, "SECRET-key", {}, None)

    provider._opener = Failed()
    with pytest.raises(ProviderError) as error:
        provider.embed(["query"], True)
    assert "SECRET-key" not in str(error.value)


def test_incompatible_model_rejected_before_network():
    from lebleg_starter.embedding import GeminiEmbedder

    with pytest.raises(ProviderError):
        GeminiEmbedder("secret", model="different")


def test_pinned_latest_pointer_checks_manifest_hash_and_download(monkeypatch, tmp_path):
    import io
    import json

    import lebleg_starter.manifest as module

    m = manifest()
    raw = json.dumps(m).encode()
    sha = "b" * 40
    pointer = {
        "format_version": 1,
        "release_id": m["release_id"],
        "manifest_path": "releases/test-1/manifest.json",
        "manifest_sha256": hashlib.sha256(raw).hexdigest(),
    }
    calls = []

    class Response(io.BytesIO):
        pass

    def open_url(url, timeout):
        calls.append(url)
        assert "/resolve/" + sha + "/" in url
        if url.endswith("latest.json"):
            return Response(json.dumps(pointer).encode())
        if url.endswith("manifest.json"):
            return Response(raw)
        return Response(b"abc")

    monkeypatch.setattr(module.urllib.request, "urlopen", open_url)
    actual, path = module.download_release(m["source"]["repo_id"], sha, "releases/latest.json", tmp_path)
    assert path.read_bytes() == b"abc"
    assert actual["artifact_revision"] == sha
    assert len(calls) == 3
    assert calls[-1].endswith("/releases/test-1/snapshot.dump")
    pointer["manifest_sha256"] = "0" * 64
    with pytest.raises(ManifestError):
        module.download_release(m["source"]["repo_id"], sha, "releases/latest.json", tmp_path)


def test_interrupted_download_removes_partial_files(monkeypatch, tmp_path):
    import io
    import json

    import lebleg_starter.manifest as module

    m = manifest()

    class Interrupted(io.BytesIO):
        def read(self, *args):
            raise RuntimeError("connection interrupted")

    def open_url(url, timeout):
        return io.BytesIO(json.dumps(m).encode()) if url.endswith("manifest.json") else Interrupted()

    monkeypatch.setattr(module.urllib.request, "urlopen", open_url)
    with pytest.raises(ManifestError):
        module.download_release(m["source"]["repo_id"], "b" * 40, "manifest.json", tmp_path)
    assert not list(tmp_path.rglob("*.partial"))


@pytest.mark.parametrize(
    "query,kind,number,year,date",
    [
        ("What does the law say about decree 123?", "decree", "123", None, None),
        ("decree 123/2005", "decree", "123", "2005", None),
        ("المرسوم الإشتراعي رقم ١٢٣ بتاريخ ٢/١/٢٠٠٥", "decree-law", "123", None, "2005-01-02"),
        ("قرار مجلس الوزراء رقم 123/2005", "cabinet-decision", "123", "2005", None),
    ],
)
def test_citation_binds_instrument_number_and_year_date(query, kind, number, year, date):
    target = citation_target(query)
    assert (target.kind, target.number, target.year, target.date) == (kind, number, year, date)


def test_query_embedding_cache_and_shared_daily_budget_before_http():
    import io
    import json

    from lebleg_starter.embedding import Gemini

    now = [100.0]
    calls = []

    class Opener:
        def open(self, request, timeout):
            calls.append(request.full_url)
            if "batchEmbedContents" in request.full_url:
                return io.BytesIO(json.dumps({"embeddings": [{"values": [1.0] * 768}]}).encode())
            return io.BytesIO(
                json.dumps(
                    {"candidates": [{"finishReason": "STOP", "content": {"parts": [{"text": '{"source_ids":[]}'}]}}]}
                ).encode()
            )

    provider = Gemini("synthetic", max_calls_per_day=2, cache_size=1, cache_ttl=3600, clock=lambda: now[0])
    provider._opener = Opener()
    first = provider.embed(["question"], query=True)
    first[0][0] = 999
    assert provider.embed(["question"], query=True)[0][0] != 999
    assert len(calls) == 1
    assert provider.select("question", [{"id": "1", "text": "original"}]) == []
    with pytest.raises(ProviderError, match="daily"):
        provider.embed(["another"], query=True)
    assert len(calls) == 2
    now[0] += 86400
    provider.embed(["another"], query=True)
    assert len(calls) == 3


def test_query_cache_expiry_and_capacity():
    import io
    import json

    from lebleg_starter.embedding import Gemini

    now = [0.0]
    calls = []

    class Opener:
        def open(self, request, timeout):
            calls.append(1)
            return io.BytesIO(json.dumps({"embeddings": [{"values": [1.0] * 768}]}).encode())

    provider = Gemini("synthetic", max_calls_per_day=10, cache_size=1, cache_ttl=5, clock=lambda: now[0])
    provider._opener = Opener()
    provider.embed(["a"], query=True)
    provider.embed(["b"], query=True)
    provider.embed(["a"], query=True)
    assert len(calls) == 3
    now[0] = 6
    provider.embed(["a"], query=True)
    assert len(calls) == 4


@pytest.mark.parametrize(
    "name,value",
    [
        ("GEMINI_MAX_CALLS_PER_DAY", "-1"),
        ("GEMINI_CONCURRENCY", "0"),
        ("GEMINI_QUERY_CACHE_SIZE", "257"),
        ("GEMINI_QUERY_CACHE_TTL", "3601"),
        ("GEMINI_MAX_CALLS_PER_DAY", "unlimited"),
    ],
)
def test_invalid_provider_guard_config_fails_closed(monkeypatch, name, value):
    from lebleg_starter.embedding import Gemini

    monkeypatch.setenv(name, value)
    with pytest.raises(ProviderError):
        Gemini("synthetic")


@pytest.mark.parametrize(
    "query,kind,number,year,date",
    [
        ("loi n° 123/2005", "law", "123", "2005", None),
        ("décret numéro 123 du 2/1/2005", "decree", "123", None, "2005-01-02"),
        ("arrêt 123/2005", "ruling", "123", "2005", None),
        ("decree 123 of 2005", "decree", "123", "2005", None),
    ],
)
def test_common_french_citations_and_bound_year(query, kind, number, year, date):
    target = citation_target(query)
    assert (target.kind, target.number, target.year, target.date) == (kind, number, year, date)


def test_zero_provider_budget_blocks_http(monkeypatch):
    from lebleg_starter.embedding import Gemini

    provider = Gemini("synthetic", max_calls_per_day=0)

    class NoHTTP:
        def open(self, *args, **kwargs):
            pytest.fail("zero budget reached HTTP")

    provider._opener = NoHTTP()
    with pytest.raises(ProviderError, match="daily"):
        provider.embed(["question"], query=True)


def test_query_cache_model_namespace_and_default_concurrency():
    import io
    import json

    from lebleg_starter.embedding import Gemini

    provider = Gemini("synthetic")
    assert provider._slots.acquire(blocking=False)
    assert provider._slots.acquire(blocking=False)
    assert not provider._slots.acquire(blocking=False)
    provider._slots.release()
    provider._slots.release()
    calls = []

    class Opener:
        def open(self, request, timeout):
            calls.append(1)
            return io.BytesIO(json.dumps({"embeddings": [{"values": [1.0] * 768}]}).encode())

    provider._opener = Opener()
    provider.embed(["query"], query=True)
    provider.model = "different-model"
    provider.embed(["query"], query=True)
    assert len(calls) == 2


@pytest.mark.parametrize(
    "query",
    [
        "law 123 (2021-01-02)",
        "law 123 issued on 2021-01-02",
        "قانون رقم 123 الصادر بتاريخ 2021-01-02",
        "loi 123, du 2/1/2021",
    ],
)
def test_explicit_date_survives_citation_punctuation_and_issuance(query):
    target = citation_target(query)
    assert (target.kind, target.number, target.date) == ("law", "123", "2021-01-02")


def test_date_belongs_to_its_bound_citation():
    target = citation_target("law 123; decree 456 issued on 2021-01-02")
    assert (target.kind, target.number, target.date) == ("law", "123", None)


def test_count_tokens_uses_grouped_parts_and_validated_total():
    import io
    import json

    from lebleg_starter.embedding import Gemini

    calls = []

    class Opener:
        def open(self, request, timeout):
            calls.append(request)
            return io.BytesIO(b'{"totalTokens":15}')

    provider = Gemini("synthetic")
    provider._opener = Opener()
    assert provider.count_tokens(["one", "two"]) == 15
    assert calls[0].full_url.endswith("/gemini-embedding-001:countTokens")
    assert json.loads(calls[0].data) == {"contents": [{"parts": [{"text": "one"}, {"text": "two"}]}]}


@pytest.mark.parametrize("value", [0, -1, True, "15", None])
def test_count_tokens_rejects_invalid_total(value):
    import io
    import json

    from lebleg_starter.embedding import Gemini

    class Opener:
        def open(self, request, timeout):
            return io.BytesIO(json.dumps({"totalTokens": value}).encode())

    provider = Gemini("synthetic")
    provider._opener = Opener()
    with pytest.raises(ProviderError):
        provider.count_tokens(["one"])


@pytest.mark.parametrize("texts", [[], [""], [" "], [None], ["one"] * 101, "one"])
def test_count_tokens_rejects_invalid_input_before_http(texts):
    from lebleg_starter.embedding import Gemini

    provider = Gemini("synthetic")

    class NoHTTP:
        def open(self, *args, **kwargs):
            pytest.fail("invalid token input reached HTTP")

    provider._opener = NoHTTP()
    with pytest.raises(ProviderError):
        provider.count_tokens(texts)


def test_optional_text_search_manifest_contract():
    m = manifest()
    m["text_search"] = {
        "extension": "pg_trgm",
        "version": "1.6",
        "indexes": [
            "source_documents_title_trgm",
            "source_documents_text_trgm",
            "source_documents_identifier",
            "source_documents_issued_on",
        ],
    }
    assert validate_manifest(m) == m
    m["text_search"]["version"] = "unsupported"
    with pytest.raises(ManifestError):
        validate_manifest(m)
