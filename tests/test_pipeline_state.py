import pytest
from lebleg_starter.pipeline_state import Ledger, content_fingerprint, worker_lock


def files(value='a', extra=False):
    rows=[{'path':'data/law.parquet','oid':value,'size':123}]
    return rows+([{'path':'README.md','oid':'changed','size':4},{'path':'releases/latest.json','oid':'changed','size':2}] if extra else [])


def test_fingerprint_ignores_metadata_but_tracks_deleted_content():
    assert content_fingerprint(files())==content_fingerprint(files(extra=True))
    assert content_fingerprint(files())!=content_fingerprint(files('b'))
    assert content_fingerprint(files()+[{'path':'data/article.parquet','oid':'x','size':8}])!=content_fingerprint(files())
    with pytest.raises(ValueError): content_fingerprint([{'path':'../data/x.parquet','oid':'x','size':8}])
    with pytest.raises(ValueError): content_fingerprint([{'path':'data/x.parquet','size':8}])


def test_seed_and_metadata_commit_do_not_build(tmp_path):
    db=Ledger(tmp_path/'state.sqlite')
    db.seed('org/data','a'*40,'1'*64,'2026-10-30.1')
    assert db.observe('org/data','b'*40,'1'*64) is None
    assert db.next_job() is None
    assert db.status()['jobs'][0]['status']=='complete'


def test_unapproved_changes_reconcile_and_approval_pins_source(tmp_path):
    db=Ledger(tmp_path/'state.sqlite')
    job=db.observe('org/data','a'*40,'1'*64)
    assert db.next_job() is None
    assert db.status()['jobs'][0]['status']=='reconcile'
    assert db.observe('org/data','a'*40,'1'*64,approved=True)==job
    assert db.next_job()['revision']=='a'*40


def test_coalesce_pending_but_keep_started_job_and_handle_reverts(tmp_path):
    db=Ledger(tmp_path/'state.sqlite')
    a=db.observe('org/data','a'*40,'1'*64,approved=True)
    b=db.observe('org/data','b'*40,'2'*64,approved=True)
    assert db.next_job()['id']==b
    assert db.job(a)['status']=='superseded'
    db.complete_stage(b,'prepare',{'ok':True})
    c=db.observe('org/data','c'*40,'1'*64,approved=True)
    assert db.next_job()['id']==b
    assert db.job(c)['status']=='pending'


def test_stage_receipt_survives_restart_and_is_ordered(tmp_path):
    path=tmp_path/'state.sqlite';db=Ledger(path)
    job=db.observe('org/data','a'*40,'1'*64,approved=True)
    db.complete_stage(job,'prepare',{'count':3})
    db=Ledger(path)
    assert db.job(job)['stage']=='index'
    assert db.receipt(job,'prepare')=={'count':3}
    with pytest.raises(ValueError):db.complete_stage(job,'promote',{})
    db.fail(job,'secret=do-not-store',now=100)
    row=db.job(job)
    assert row['error']=='stage_failed' and 'do-not-store' not in str(db.status())
    assert db.next_job(now=100) is None
    assert db.next_job(now=1000)['id']==job


def test_lanes_require_exact_fingerprint(tmp_path):
    db=Ledger(tmp_path/'state.sqlite');db.seed('org/data','a'*40,'1'*64,'r1')
    db.lane('website','org/data','0'*64,required=True)
    assert db.status()['freshness']=='partial'
    db.lane('website','org/data','1'*64,required=True)
    assert db.status()['freshness']=='current'
    db.observe('org/data','b'*40,'2'*64)
    assert db.status()['freshness']=='partial'


def test_worker_lock_excludes_another_run_and_releases(tmp_path):
    with worker_lock(tmp_path/'lock') as held:
        assert held
        with worker_lock(tmp_path/'lock') as other:assert not other
    with worker_lock(tmp_path/'lock') as held:assert held
