"""Real PG build/restore and published images; Hub/provider are isolated fixtures."""
import hashlib
import json
import os
import sys
from datetime import date
from pathlib import Path
from types import SimpleNamespace

import pytest
from test_build import db as db  # noqa: PLC0414
from test_pipeline_publish import Hub

from lebleg_starter.pipeline import Coordinator
from lebleg_starter.pipeline_steps import ReleaseSteps


class FixtureHub(Hub):
    def list_repo_tree(self,repo,repo_type,revision,recursive):
        return [SimpleNamespace(path=p,blob_id=hashlib.sha1(v,usedforsecurity=False).hexdigest(),size=len(v),
                    lfs=SimpleNamespace(sha256=hashlib.sha256(v).hexdigest())) for p,v in self.files.items()]


def test_real_pipeline_resumes_after_public_check_failure_without_reembedding(db,tmp_path,monkeypatch):
    dump_bin=os.getenv('LEBLEG_TEST_PGDUMP');restore_bin=os.getenv('LEBLEG_TEST_PGRESTORE')
    if not dump_bin or not restore_bin or not os.getenv('LEBLEG_PIPELINE_IMAGE_TEST'):
        pytest.skip('enable actual PG18 clients and Docker image integration')
    import huggingface_hub
    import pyarrow as pa
    import pyarrow.parquet as pq

    from lebleg_starter import embedding, publish
    from lebleg_starter.sources import resource

    db.execute(resource('public_schema.sql').split('ALTER TABLE',1)[0])
    definitions=db.execute("SELECT table_name,column_name,udt_name FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position").fetchall()
    types={'text':pa.string(),'jsonb':pa.string(),'int2':pa.int16(),'int4':pa.int32(),'int8':pa.int64(),'float4':pa.float32(),
           'date':pa.date32(),'timestamptz':pa.timestamp('us',tz='UTC'),'bool':pa.bool_(),'_text':pa.list_(pa.string()),'_int4':pa.list_(pa.int32())}
    schemas={}
    for table,column,typ in definitions:schemas.setdefault(table,[]).append(pa.field(column,types[typ]))
    db.rollback()
    samples={
        'legislation_kind':[{'name_ar':'قانون','name_en':'Law'}],
        'legislation':[{'id':1,'kind':'قانون','title':'قانون اختبار','number':'10','status':'in_force','issued_on':date(2020,1,1)}],
        'legislation_article':[{'law_id':1,'position':1,'number':'1','text_ar':'نص أصلي للاختبار'}],
        'article_in_force':[{'law_id':1,'position':1,'number':'1','text_ar':'نص أصلي للاختبار','state':'in_force','pending':False}],
        'court':[{'name_ar':'محكمة','name_en':'Court'}],
        'ruling':[{'id':8,'court':'محكمة','title':'حكم اختبار','number':'7','year':2020}],
        'ruling_page':[{'ruling_id':8,'n':1,'text_ar':'قراءة آلية للاختبار','model':'fixture','low_confidence':True}],
        'ruling_file':[{'ruling_id':8,'pages':1}],
        'gazette_issue':[{'id':1,'number':'2','published_on':date(2020,1,2)}],
        'gazette_entry':[{'issue_id':1,'law_id':1}],
    }
    hub=FixtureHub();hub.files['README.md']=b'---\nlicense: other\n---\n\n# Fixture\n\nCredit the source.\n'
    hub.files['releases/latest.json']=b'{"release_id":"baseline"}'
    inputs=tmp_path/'inputs';inputs.mkdir()
    for table,fields in schemas.items():
        path=inputs/(table+'.parquet')
        pq.write_table(pa.Table.from_pylist(samples.get(table,[]),schema=pa.schema(fields)),path)
        hub.files['data/'+path.name]=path.read_bytes()
    def download(repo,repo_type,revision,allow_patterns,local_dir,token):
        data=Path(local_dir)/'data';data.mkdir(parents=True,exist_ok=True)
        for path in inputs.glob('*.parquet'):(data/path.name).write_bytes(path.read_bytes())
    monkeypatch.setattr(huggingface_hub,'snapshot_download',download)
    monkeypatch.setattr(huggingface_hub,'HfApi',lambda token=None:hub)
    monkeypatch.setattr(publish,'_client',lambda api,token=None:api or hub)
    class Provider:
        model='gemini-embedding-001'
        batches=0
        def __init__(self,key):pass
        def count_tokens(self,texts):return sum(len(t.encode()) for t in texts)
        def embed(self,texts,query=False):
            Provider.batches+=1
            return [[1.0]+[0.0]*767 for _ in texts]
    monkeypatch.setattr(embedding,'GeminiEmbedder',Provider)
    monkeypatch.setenv('GEMINI_API_KEY','fixture-only')
    monkeypatch.setenv('PIPELINE_ADMIN_DSN',os.environ['LEBLEG_TEST_ADMIN_DSN'])
    monkeypatch.delenv('PIPELINE_REUSE_DSN',raising=False)
    monkeypatch.setenv('PIPELINE_READER_PASSWORD','fixture-reader')
    monkeypatch.setenv('PIPELINE_SMOKE_SEMANTIC','0')
    monkeypatch.setenv('PG_CLIENT_MOUNT','/tmp')
    monkeypatch.setenv('PYTHONPATH',str(Path(__file__).parents[1]/'src'))
    for kind in ('API','MCP'):
        monkeypatch.setenv('PIPELINE_'+kind+'_IMAGE',os.getenv('LEBLEG_TEST_'+kind+'_IMAGE',f'ghcr.io/taktekhq/lebanese-legislation-{kind.lower()}:0.1.4'))
    settings={'state_dir':str(tmp_path/'state'),'sources':[{'repo':'org/data','kind':'corpus'}],
        'minimum_free_bytes':0,'pg_dump':dump_bin,'pg_restore':restore_bin,
        'smoke_command':[sys.executable,'-m','lebleg_starter.pipeline_smoke']}
    co=Coordinator(settings,hub=hub);co.detect();job=co.ledger.status()['jobs'][0]
    co.approve(job['repo'],job['revision'],job['fingerprint']);steps=ReleaseSteps(co)
    failures=[]
    def fail_check(stage,job):
        if stage=='check':hub.public=False
        try:
            return steps(stage,job)
        except Exception as error:
            failures.append((stage,type(error).__name__,str(error)))
            raise
    try:
        assert not co.run(fail_check)
        assert co.ledger.job(job['id'])['stage']=='check', failures
        assert json.loads(hub.files['releases/latest.json'])['release_id']=='baseline'
        batches=Provider.batches
        assert batches>0
        hub.public=True;co=Coordinator(settings,hub=hub);co.ledger.retry(job['id'])
        assert co.run()
        assert Provider.batches==batches
        assert json.loads(hub.files['releases/latest.json'])['release_id']==job['release_id']
        assert co.ledger.status()['freshness']=='current'
        assert co.detect()==0
        assert b'Credit the source.' in hub.files['README.md']
    finally:
        steps.drop_owned_database(job,verify=True);steps.drop_owned_database(job)
