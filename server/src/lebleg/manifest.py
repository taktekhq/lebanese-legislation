"""Versioned public release contract and verified atomic downloads."""

import hashlib
import json
import os
import re
import tempfile
import urllib.request
from pathlib import Path, PurePosixPath
from urllib.parse import quote


class ManifestError(ValueError):
    pass


def safe_path(value):
    if not isinstance(value, str) or not value or "\\" in value or ":" in value:
        raise ManifestError("invalid artifact path")
    p = PurePosixPath(value)
    if p.is_absolute() or any(x in ("", ".", "..") for x in value.split("/")):
        raise ManifestError("invalid artifact path")
    return value


def _require(condition):
    if not condition:
        raise ManifestError("unsupported or invalid release manifest")


def validate_manifest(m):
    try:
        _require(m["format_version"] == 1)
        _require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", m["release_id"]))
        _require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*", m["source"]["repo_id"]))
        _require(re.fullmatch(r"[0-9a-f]{40}", m["source"]["revision"]))
        _require(m["postgres"] == {"version": "18.6", "vector_version": "0.8.2"})
        if "text_search" in m:
            _require(
                m["text_search"]
                == {
                    "extension": "pg_trgm",
                    "version": "1.6",
                    "indexes": [
                        "source_documents_title_trgm",
                        "source_documents_text_trgm",
                        "source_documents_identifier",
                        "source_documents_issued_on",
                    ],
                }
            )

        _require(
            {k: v for k, v in m["embedding"].items() if k not in ("storage", "provider")}
            == {
                "model": "gemini-embedding-001",
                "dimensions": 768,
                "task_type": "RETRIEVAL_DOCUMENT",
                "chunk_size": 900,
                "chunk_overlap": 120,
            }
        )
        _require(m["embedding"].get("storage", "halfvec(768)") == "halfvec(768)")
        _require(m["embedding"].get("provider", "gemini") == "gemini")
        safe_path(m["dump"]["path"])
        _require(type(m["dump"]["size_bytes"]) is int and m["dump"]["size_bytes"] > 0)
        _require(re.fullmatch(r"[0-9a-f]{64}", m["dump"]["sha256"]))
        for key in ("source_documents", "semantic_chunks"):
            _require(type(m["counts"][key]) is int and m["counts"][key] >= 0)
        for name, count in m["counts"]["tables"].items():
            _require(re.fullmatch(r"[a-z_][a-z0-9_]*", name))
            _require(type(count) is int and count >= 0)
        _require(m["generated_at"] and m["build_version"] and m["dataset"]["license"] and m["dataset"]["attribution"])
    except (KeyError, TypeError, AttributeError):
        raise ManifestError("unsupported or invalid release manifest") from None
    return m


def verify_file(path, artifact):
    digest = hashlib.sha256()
    size = 0
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
            size += len(block)
    if size != artifact["size_bytes"] or digest.hexdigest() != artifact["sha256"]:
        raise ManifestError("artifact size or checksum mismatch")


def load_manifest(path):
    with open(path, encoding="utf-8") as stream:
        return validate_manifest(json.load(stream))


def _bytes_url(url):
    try:
        with urllib.request.urlopen(url, timeout=45) as response:
            raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ManifestError("release metadata exceeds size limit")
            return raw
    except (OSError, ValueError):
        raise ManifestError("release metadata download failed") from None


def _json_url(url):
    try:
        return json.loads(_bytes_url(url))
    except (ValueError, TypeError):
        raise ManifestError("invalid release metadata") from None


def resolve_revision(repo_id, revision="main"):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*", repo_id):
        raise ManifestError("invalid dataset repository")
    if re.fullmatch(r"[0-9a-f]{40}", revision):
        return revision
    info = _json_url(
        f"https://huggingface.co/api/datasets/{quote(repo_id, safe='/')}/revision/{quote(revision, safe='')}"
    )
    sha = info.get("sha", "")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ManifestError("dataset revision did not resolve to a commit")
    return sha


def download_release(repo_id, revision, manifest_path, cache_dir):
    sha = resolve_revision(repo_id, revision)
    safe_path(manifest_path)
    base = f"https://huggingface.co/datasets/{quote(repo_id, safe='/')}/resolve/{sha}/"
    candidate = _json_url(base + quote(manifest_path, safe="/"))
    _require(isinstance(candidate, dict))
    if "dump" not in candidate:
        try:
            _require(candidate["format_version"] == 1)
            safe_path(candidate["manifest_path"])
            _require(re.fullmatch(r"[0-9a-f]{64}", candidate["manifest_sha256"]))
            manifest_path = candidate["manifest_path"]
            raw = _bytes_url(base + quote(manifest_path, safe="/"))
            _require(hashlib.sha256(raw).hexdigest() == candidate["manifest_sha256"])
            m = validate_manifest(json.loads(raw))
            _require(m["release_id"] == candidate["release_id"])
        except (KeyError, TypeError, ValueError):
            raise ManifestError("invalid release pointer or manifest checksum") from None
    else:
        m = validate_manifest(candidate)
    m = {**m, "artifact_revision": sha}
    # The manifest source revision identifies the Parquet input, while sha pins the release artifacts.
    root = Path(cache_dir).resolve() / sha
    root.mkdir(parents=True, exist_ok=True)
    artifact_path = str(PurePosixPath(manifest_path).parent / m["dump"]["path"])
    target = root / hashlib.sha256(artifact_path.encode()).hexdigest()
    if target.exists():
        verify_file(target, m["dump"])
        return m, target
    fd, temporary = tempfile.mkstemp(prefix="download-", suffix=".partial", dir=root)
    try:
        with (
            os.fdopen(fd, "wb") as out,
            urllib.request.urlopen(base + quote(artifact_path, safe="/"), timeout=60) as response,
        ):
            size = 0
            while block := response.read(1024 * 1024):
                size += len(block)
                if size > m["dump"]["size_bytes"]:
                    raise ManifestError("artifact exceeds declared size")
                out.write(block)
            out.flush()
            os.fsync(out.fileno())
        verify_file(temporary, m["dump"])
        os.replace(temporary, target)
    except Exception:  # noqa: BLE001 - every failed stream must remove its partial file.
        Path(temporary).unlink(missing_ok=True)
        raise ManifestError("artifact download or verification failed") from None
    return m, target
