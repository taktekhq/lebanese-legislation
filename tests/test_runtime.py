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
    assert citation_target("law 123 dated 2020-01-02") == ("123", "2020-01-02")
    assert citation_target("قانون رقم ١٢٣") == ("123", None)
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
