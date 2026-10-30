"""Publish a verified immutable release without replacing the Parquet corpus."""

import argparse
import hashlib
import json
import os
from pathlib import Path

from .manifest import load_manifest, safe_path, verify_file


def plan_release(directory):
    directory = Path(directory)
    manifest_path = directory / "manifest.json"
    m = load_manifest(manifest_path)
    dump = directory / safe_path(m["dump"]["path"])
    verify_file(dump, m["dump"])
    prefix = f"releases/{m['release_id']}"
    raw = manifest_path.read_bytes()
    pointer = {
        "format_version": 1,
        "release_id": m["release_id"],
        "manifest_path": f"{prefix}/manifest.json",
        "manifest_sha256": hashlib.sha256(raw).hexdigest(),
    }
    return {
        f"{prefix}/manifest.json": raw,
        f"{prefix}/{m['dump']['path']}": dump,
        "releases/latest.json": (json.dumps(pointer, indent=2) + "\n").encode(),
    }


def publish(directory, repo_id, token=None):
    from huggingface_hub import CommitOperationAdd, HfApi, hf_hub_download

    api = HfApi(token=token)
    files = plan_release(directory)
    head = api.repo_info(repo_id, repo_type="dataset").sha
    existing = set(api.list_repo_files(repo_id, repo_type="dataset", revision=head))
    immutable = [name for name in files if name != "releases/latest.json"]
    if any(name in existing for name in immutable):
        # Never overwrite an already published release, even on a retry.
        raise ValueError("release ID already exists; choose a new release ID")
    attributes = b""
    if ".gitattributes" in existing:
        attributes = Path(
            hf_hub_download(repo_id, ".gitattributes", repo_type="dataset", revision=head, token=token)
        ).read_bytes()
    if b"*.dump filter=lfs" not in attributes:
        files[".gitattributes"] = attributes.rstrip() + b"\n*.dump filter=lfs diff=lfs merge=lfs -text\n"
    operations = [
        CommitOperationAdd(path_in_repo=name, path_or_fileobj=str(value) if isinstance(value, Path) else value)
        for name, value in files.items()
    ]
    result = api.create_commit(
        repo_id,
        repo_type="dataset",
        parent_commit=head,
        operations=operations,
        commit_message="Publish verified PostgreSQL and pgvector release",
    )
    return result.oid


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--repo", default="taktekhq/lebanese-legislation")
    args = parser.parse_args(argv)
    revision = publish(args.directory, args.repo, os.getenv("HF_TOKEN"))
    print(json.dumps({"artifact_revision": revision}))
    return 0
