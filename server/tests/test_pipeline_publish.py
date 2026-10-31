import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

import pytest

from lebleg.publish import promote_candidate, upload_candidate, verify_candidate


class Hub:
    def __init__(self):
        self.files={'.gitattributes':b'*.parquet filter=lfs diff=lfs merge=lfs -text\n'}
        self.sha='a'*40
        self.commits=0
        self.public=True

    def repo_info(self, repo, repo_type):return SimpleNamespace(sha=self.sha)
    def list_repo_files(self, repo, repo_type, revision):return list(self.files)
    def read(self, repo, path, revision):
        if not self.public:raise PermissionError('private')
        return self.files[path]
    def get_paths_info(self, repo, paths, repo_type, revision):
        data=self.files[paths[0]]
        return [SimpleNamespace(size=len(data),lfs=SimpleNamespace(sha256=hashlib.sha256(data).hexdigest()))]
    def create_commit(self, repo, repo_type, parent_commit, operations, commit_message):
        assert parent_commit==self.sha
        for op in operations:
            value=op.path_or_fileobj
            self.files[op.path_in_repo]=Path(value).read_bytes() if isinstance(value,str) else value
        self.commits+=1;self.sha=str(self.commits)*40
        return SimpleNamespace(oid=self.sha)


@pytest.fixture
def release(tmp_path):
    from test_runtime import manifest
    m=manifest();data=b'postgres snapshot payload'
    m['dump']={'path':'corpus.dump','size_bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}
    (tmp_path/'manifest.json').write_text(json.dumps(m));(tmp_path/'corpus.dump').write_bytes(data)
    return tmp_path,m


def test_candidate_never_promotes_and_retry_accepts_only_identical(release):
    directory,m=release;hub=Hub()
    sha=upload_candidate(directory,'org/data',api=hub)
    assert 'releases/latest.json' not in hub.files
    assert upload_candidate(directory,'org/data',api=hub)==sha
    assert hub.commits==1
    hub.files[f"releases/{m['release_id']}/manifest.json"]=b'wrong'
    with pytest.raises(ValueError):upload_candidate(directory,'org/data',api=hub)


def test_failed_public_check_keeps_old_pointer(release):
    directory,m=release;hub=Hub();sha=upload_candidate(directory,'org/data',api=hub)
    hub.files['releases/latest.json']=b'{"release_id":"old"}'
    hub.public=False
    with pytest.raises((ValueError,PermissionError)):
        promote_candidate('org/data',sha,m,'old',api=hub)
    assert json.loads(hub.files['releases/latest.json'])['release_id']=='old'


def test_promotion_refuses_newer_pointer_and_is_idempotent(release):
    directory,m=release;hub=Hub();sha=upload_candidate(directory,'org/data',api=hub)
    hub.files['releases/latest.json']=b'{"release_id":"newer"}'
    with pytest.raises(ValueError):promote_candidate('org/data',sha,m,'old',api=hub)
    hub.files['releases/latest.json']=b'{"release_id":"old"}'
    promoted=promote_candidate('org/data',sha,m,'old',api=hub)
    pointer=json.loads(hub.files['releases/latest.json'])
    assert pointer['release_id']==m['release_id']
    assert promote_candidate('org/data',sha,m,'old',api=hub)==promoted


def test_public_candidate_checks_remote_identity(release):
    directory,m=release;hub=Hub();sha=upload_candidate(directory,'org/data',api=hub)
    assert verify_candidate('org/data',sha,m,api=hub)['dump_sha256']==m['dump']['sha256']
    hub.files[f"releases/{m['release_id']}/corpus.dump"]=b'corrupt'
    with pytest.raises(ValueError):verify_candidate('org/data',sha,m,api=hub)
