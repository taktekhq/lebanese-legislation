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


def _client(api, token=None):
    if api is not None:
        return api
    from huggingface_hub import HfApi
    return HfApi(token=token)


def _read(api, repo, path, revision):
    if hasattr(api, 'read'):
        return api.read(repo, path, revision)
    from huggingface_hub import hf_hub_download
    return Path(hf_hub_download(repo, path, repo_type='dataset', revision=revision, token=api.token)).read_bytes()


def _dump_identity(api, repo, path, revision, manifest):
    values = api.get_paths_info(repo, [path], repo_type='dataset', revision=revision)
    if len(values) != 1 or values[0].size != manifest['dump']['size_bytes'] or not values[0].lfs or values[0].lfs.sha256 != manifest['dump']['sha256']:
        raise ValueError('candidate dump identity mismatch')


def upload_candidate(directory, repo_id, token=None, *, api=None):
    """Upload immutable artifacts without changing the public latest pointer."""
    from huggingface_hub import CommitOperationAdd

    api = _client(api, token)
    files = {k: v for k, v in plan_release(directory).items() if k != 'releases/latest.json'}
    manifest = load_manifest(Path(directory) / 'manifest.json')
    head = api.repo_info(repo_id, repo_type='dataset').sha
    existing = set(api.list_repo_files(repo_id, repo_type='dataset', revision=head))
    present = set(files) & existing
    manifest_path = f"releases/{manifest['release_id']}/manifest.json"
    dump_path = f"releases/{manifest['release_id']}/{manifest['dump']['path']}"
    if present:
        if present != set(files) or _read(api, repo_id, manifest_path, head) != files[manifest_path]:
            raise ValueError('immutable candidate already exists with another identity')
        _dump_identity(api, repo_id, dump_path, head, manifest)
        return head
    attributes = _read(api, repo_id, '.gitattributes', head) if '.gitattributes' in existing else b''
    if b'*.dump filter=lfs' not in attributes:
        files['.gitattributes'] = attributes.rstrip() + b'\n*.dump filter=lfs diff=lfs merge=lfs -text\n'
    operations = [CommitOperationAdd(path_in_repo=p, path_or_fileobj=str(v) if isinstance(v, Path) else v)
                  for p, v in files.items()]
    return api.create_commit(repo_id, repo_type='dataset', parent_commit=head, operations=operations,
                             commit_message='Upload verified database release candidate').oid


def verify_candidate(repo_id, revision, manifest, *, api=None, local_dump=None):
    """Verify immutable candidate metadata and anonymous sample downloads."""
    from .manifest import validate_manifest

    validate_manifest(manifest)
    api = _client(api, False)
    prefix = f"releases/{manifest['release_id']}"
    raw = _read(api, repo_id, prefix + '/manifest.json', revision)
    if json.loads(raw) != {k: v for k, v in manifest.items() if k != 'artifact_revision'}:
        raise ValueError('candidate manifest mismatch')
    path = prefix + '/' + manifest['dump']['path']
    _dump_identity(api, repo_id, path, revision, manifest)
    size = manifest['dump']['size_bytes']
    for start, end in [(0, min(size, 65536) - 1), (max(0, size - 65536), size - 1)]:
        if hasattr(api, 'read'):
            data = api.read(repo_id, path, revision)[start:end + 1]
        else:
            import urllib.request
            from urllib.parse import quote
            url = f"https://huggingface.co/datasets/{quote(repo_id, safe='/')}/resolve/{revision}/{quote(path, safe='/')}"
            request = urllib.request.Request(url, headers={'Range': f'bytes={start}-{end}'})
            with urllib.request.urlopen(request, timeout=120) as response:
                if response.status != 206:
                    raise ValueError('candidate range download failed')
                data = response.read(end - start + 2)
        if len(data) != end - start + 1:
            raise ValueError('candidate range length mismatch')
        if local_dump:
            with open(local_dump, 'rb') as stream:
                stream.seek(start)
                if stream.read(len(data)) != data:
                    raise ValueError('candidate download differs from local dump')
    return {'manifest_sha256': hashlib.sha256(raw).hexdigest(), 'dump_sha256': manifest['dump']['sha256'],
            'artifact_revision': revision, 'public': True}


def promote_candidate(repo_id, revision, manifest, expected_previous_release, token=None, *, api=None):
    """Promote a publicly verified candidate, refusing to replace a newer release."""
    from huggingface_hub import CommitOperationAdd

    # Production verification always uses a separate anonymous client.
    proof = verify_candidate(repo_id, revision, manifest, api=api)
    api = _client(api, token)
    head = api.repo_info(repo_id, repo_type='dataset').sha
    existing = set(api.list_repo_files(repo_id, repo_type='dataset', revision=head))
    current = json.loads(_read(api, repo_id, 'releases/latest.json', head)) if 'releases/latest.json' in existing else {}
    if current.get('release_id') == manifest['release_id']:
        if current.get('manifest_sha256') != proof['manifest_sha256']:
            raise ValueError('existing pointer identity mismatch')
        return head
    if current.get('release_id') != expected_previous_release:
        raise ValueError('latest pointer changed; reconcile before promotion')
    pointer = {'format_version': 1, 'release_id': manifest['release_id'],
               'manifest_path': f"releases/{manifest['release_id']}/manifest.json",
               'manifest_sha256': proof['manifest_sha256']}
    return api.create_commit(repo_id, repo_type='dataset', parent_commit=head,
                             operations=[CommitOperationAdd(path_in_repo='releases/latest.json',
                                 path_or_fileobj=(json.dumps(pointer, indent=2) + '\n').encode())],
                             commit_message='Promote publicly verified database release').oid
