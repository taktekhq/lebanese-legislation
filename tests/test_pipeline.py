import json

import pytest

from lebleg_starter.pipeline import Coordinator, load_config, run_adapter
from lebleg_starter.pipeline_state import STAGES


class Hub:
    def __init__(self):self.revision='a'*40;self.oid='1'*40
    def repo_info(self,repo,repo_type):
        from types import SimpleNamespace
        return SimpleNamespace(sha=self.revision)
    def list_repo_tree(self,repo,repo_type,revision,recursive):
        from types import SimpleNamespace
        return [SimpleNamespace(path='data/law.parquet',blob_id=self.oid,size=2,lfs=None)]


def config(tmp_path):
    return {'state_dir':str(tmp_path),'sources':[{'repo':'org/data','kind':'corpus'}], 'daily_embedding_usd':'1'}


def test_detection_deduplicates_and_requires_trusted_approval(tmp_path):
    hub=Hub();co=Coordinator(config(tmp_path),hub=hub)
    assert co.detect()==1
    assert co.ledger.next_job() is None
    assert co.detect()==0
    job=co.ledger.status()['jobs'][0]
    co.approve('org/data',job['revision'],job['fingerprint'])
    assert co.ledger.next_job()['revision']==hub.revision
    hub.revision='b'*40
    assert co.detect()==0


def test_restart_retries_only_failed_stage_and_holds_newer_job(tmp_path):
    co=Coordinator(config(tmp_path),hub=Hub());co.detect()
    job=co.ledger.status()['jobs'][0];co.approve('org/data',job['revision'],job['fingerprint'])
    calls=[]
    def handler(stage,job):
        calls.append(stage)
        if stage=='index':raise RuntimeError('DSN=private-password')
        return {'ok':True}
    assert co.run(handler)==False
    assert calls==['prepare','index']
    assert 'private-password' not in json.dumps(co.ledger.status())
    co=Coordinator(config(tmp_path),hub=Hub());co.ledger.retry(job['id'])
    calls.clear()
    assert co.run(lambda stage,job:calls.append(stage) or {'ok':True})
    assert calls==list(STAGES[1:])
    assert co.ledger.status()['freshness']=='current'


def test_seed_checks_current_release_without_enqueuing(tmp_path):
    co=Coordinator(config(tmp_path),hub=Hub())
    co.seed('org/data','a'*40,'r1')
    assert co.detect()==0
    assert co.ledger.next_job() is None


def test_adapter_timeout_and_invalid_receipt_are_safe():
    with pytest.raises(ValueError):run_adapter(['python3','-c','print("not-json")'],{},timeout=5)
    with pytest.raises(ValueError):run_adapter(['python3','-c','import time;time.sleep(5)'],{},timeout=0.01)
    assert run_adapter(['python3','-c','print(\'{"ok":true}\')'],{},timeout=5)=={'ok':True}


def test_private_config_requires_restricted_env_file(tmp_path):
    env=tmp_path/'private.env';env.write_text('TOKEN=value\n');env.chmod(0o644)
    path=tmp_path/'config.json';path.write_text(json.dumps({**config(tmp_path),'env_file':str(env)}))
    with pytest.raises(ValueError):load_config(path)


def test_scans_seed_is_also_a_noop(tmp_path):
    settings=config(tmp_path);settings['sources'][0]['kind']='scans'
    co=Coordinator(settings,hub=Hub());co.seed('org/data','a'*40,'baseline')
    assert co.detect()==0


def test_approval_cannot_rewind_current_content(tmp_path):
    hub=Hub();co=Coordinator(config(tmp_path),hub=hub);co.detect()
    job=co.ledger.status()['jobs'][0]
    hub.oid='2'*40;hub.revision='b'*40;co.detect()
    with pytest.raises(ValueError):co.approve('org/data',job['revision'],job['fingerprint'])


def test_secret_files_are_restricted_and_read_without_shell(tmp_path,monkeypatch):
    secret=tmp_path/'secret';secret.write_text('not-for-logs\n');secret.chmod(0o600)
    path=tmp_path/'config.json';path.write_text(json.dumps({**config(tmp_path),'secret_files':{'PIPELINE_TEST_TOKEN':str(secret)}}))
    load_config(path)
    import os
    assert os.environ['PIPELINE_TEST_TOKEN']=='not-for-logs'
    secret.chmod(0o644)
    with pytest.raises(ValueError):load_config(path)
    monkeypatch.delenv('PIPELINE_TEST_TOKEN')


def test_adapter_receipt_cannot_persist_arbitrary_error_or_secret_text():
    command=['python3','-c','print(\'{"ok":true,"status":"password=secret"}\')']
    with pytest.raises(ValueError):run_adapter(command,{},timeout=5)


def test_cleanup_only_removes_labeled_pipeline_smoke_containers(monkeypatch):
    from types import SimpleNamespace

    from lebleg_starter.pipeline_smoke import cleanup_smoke
    owner='a'*64;calls=[]
    def docker(command,**kwargs):
        calls.append(command)
        if command[1:3]==['ps','-aq']:return SimpleNamespace(returncode=0,stdout='1'*64+'\n'+'2'*64+'\n')
        if command[1]=='inspect':
            name='/lebleg-pipeline-smoke-fixture' if command[-1].startswith('1') else '/another-session'
            return SimpleNamespace(returncode=0,stdout=json.dumps([{'Name':name,'Config':{'Labels':{'org.lebleg.pipeline.owner':owner}}}]))
        return SimpleNamespace(returncode=0,stdout='')
    monkeypatch.setattr('subprocess.run',docker)
    cleanup_smoke(owner)
    removals=[c for c in calls if c[1:3]==['rm','-f']]
    assert removals==[['docker','rm','-f','1'*64]]
