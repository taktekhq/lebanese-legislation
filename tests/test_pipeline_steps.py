import json

import pytest
from test_pipeline import Hub, config

from lebleg_starter.pipeline import Coordinator
from lebleg_starter.pipeline_steps import ReleaseSteps


def test_steps_refuse_unconfigured_database_without_starting_a_build(tmp_path,monkeypatch):
    co=Coordinator(config(tmp_path),hub=Hub());co.detect();job=co.ledger.status()['jobs'][0]
    co.approve('org/data',job['revision'],job['fingerprint']);steps=ReleaseSteps(co)
    monkeypatch.delenv('PIPELINE_ADMIN_DSN',raising=False)
    with pytest.raises(ValueError):steps._dsn(job,create=True)


def test_scan_metadata_never_invokes_corpus_build(tmp_path,monkeypatch):
    settings=config(tmp_path);settings['sources'][0]['kind']='scans'
    co=Coordinator(settings,hub=Hub());co.detect();job=co.ledger.next_job()
    steps=ReleaseSteps(co)
    monkeypatch.setattr(steps,'prepare_scans',lambda job:{'ok':True,'counts':{'documents':3}})
    assert steps('prepare',job)['counts']['documents']==3
    for stage in ['index','validate','export','upload','check','promote']:
        assert steps(stage,job)=={'ok':True,'kind':'scans'}


def test_metadata_template_overlay_is_atomic_and_does_not_edit_unmanaged_text(tmp_path):
    settings=config(tmp_path);settings['sources'][0]['card_block_file']=str(tmp_path/'block.md')
    co=Coordinator(settings,hub=Hub());steps=ReleaseSteps(co)
    steps.write_overlay(settings['sources'][0],'**Database:** ready')
    assert (tmp_path/'block.md').read_text()=='**Database:** ready\n'
    assert (tmp_path/'block.md').stat().st_mode & 0o777==0o600


def test_export_recovery_never_replaces_an_existing_dump(tmp_path):
    # A committed dump may survive a crash before its external manifest write.
    from test_runtime import manifest

    from lebleg_starter.pipeline_steps import finish_export
    m=manifest();m.pop('dump');(tmp_path/'corpus.dump').write_bytes(b'complete')
    finish_export('unused',tmp_path,m,'/bin/false')
    assert json.loads((tmp_path/'manifest.json').read_text())['dump']['size_bytes']==8
    assert (tmp_path/'corpus.dump').read_bytes()==b'complete'


def test_cleanup_keeps_current_previous_and_unfinished_jobs():
    from lebleg_starter.pipeline_steps import retired_jobs
    def row(i,status='complete',repo='org/data'):
        return {'id':i,'repo':repo,'status':status,'release_id':'auto-'+str(i)}
    current=row(5,'pending')
    jobs=[row(1),row(2),row(3,'retry'),row(4),current,row(6,repo='org/other')]
    assert [r['id'] for r in retired_jobs(current,jobs)]==[1,2]


def test_scan_counts_are_checked_against_parquet_rows(tmp_path):
    import pyarrow as pa
    import pyarrow.parquet as pq

    from lebleg_starter.pipeline_steps import check_scan_counts
    documents=tmp_path/'documents.parquet';pages=tmp_path/'pages.parquet'
    pq.write_table(pa.table({'id':[1,2]}),documents);pq.write_table(pa.table({'id':[1,2,3]}),pages)
    check_scan_counts({'documents':documents,'pages':pages},{'documents':2,'pages':3})
    with pytest.raises(ValueError):check_scan_counts({'documents':documents,'pages':pages},{'documents':3,'pages':3})
