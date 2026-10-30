"""Consolidated resources expose verified derived wording without changing raw fields."""
import json

import psycopg
import pytest
from fastapi.testclient import TestClient
from psycopg.types.json import Jsonb
from test_calculated_current import record
from test_general_postgres import general  # noqa: F401

from lebleg_starter.server import create_app


def seed(service):
    row=record()
    with psycopg.connect(service.search_service.pool.conninfo) as conn:
        conn.execute("INSERT INTO legislation(id,kind,number,issued_on,title,status) VALUES(244565,'قانون','90','1983-01-01','أصول المحاكمات','in_force')")
        conn.execute("INSERT INTO legislation_article(law_id,position,number,text_ar,text_fr) VALUES(244565,86,'86',%s,'texte original')",(row['calculated_current']['source_text_ar'],))
        conn.execute("INSERT INTO article_in_force(law_id,position,number,text_ar,state,pending,valid_from) VALUES(244565,86,'86',%s,'in_force',false,'2000-01-01')",(row['calculated_current']['source_text_ar'],))
        conn.execute('INSERT INTO article_calculated_current VALUES(%s,%s,%s,%s,%s)',(row['law_id'],row['number'],row['position'],row['source_sha256'],Jsonb(row['calculated_current'])))
    return row


def test_article_calculation_direct_http_and_mcp_preserve_original(general):  # noqa: F811
    row=seed(general)
    out=general.article(244565,'86')
    assert out['calculated_current']==row['calculated_current']
    assert out['text_ar']==row['calculated_current']['source_text_ar']
    assert out['text_fr']=='texte original'
    with TestClient(create_app(service=general.search_service,general_service=general)) as client:
        assert client.get('/api/v1/law/244565/article/86/').json()==out
        response=client.post('/mcp',json={'jsonrpc':'2.0','id':1,'method':'tools/call','params':{'name':'get_article','arguments':{'law_id':244565,'number':'86'}}}).json()
        assert json.loads(response['result']['content'][0]['text'])['result']==out


@pytest.mark.parametrize('mutation', [
    "UPDATE legislation_article SET text_ar='changed' WHERE law_id=244565",
    "UPDATE article_in_force SET text_ar='changed' WHERE law_id=244565",
    "UPDATE article_calculated_current SET source_sha256='stale'",
    "UPDATE article_calculated_current SET calculated_current=jsonb_set(calculated_current,'{authority,article}','\"2\"')",
    "UPDATE article_in_force SET pending=true WHERE law_id=244565",
    "UPDATE legislation_article SET in_force_from='2099-01-01' WHERE law_id=244565",
    "UPDATE article_in_force SET valid_from='2099-01-01' WHERE law_id=244565",
    "UPDATE article_in_force SET state='repealed' WHERE law_id=244565",
    "UPDATE legislation SET status='annulled' WHERE id=244565",
    "UPDATE legislation SET issued_on='2099-01-01' WHERE id=244565",
    "INSERT INTO article_version(law_id,article_number,major,minor,state,text_ar) VALUES(244565,'86',1,0,'in_force','later wording')",
    "INSERT INTO article_version(law_id,article_number,major,minor,state,text_ar) SELECT 244565,'86',1,0,'repealed',text_ar FROM legislation_article WHERE law_id=244565",
    "INSERT INTO article_version(law_id,article_number,major,minor,state,valid_from,text_ar) SELECT 244565,'86',1,0,'in_force','2099-01-01',text_ar FROM legislation_article WHERE law_id=244565",
    'DELETE FROM article_calculated_current',
    'DROP TABLE article_calculated_current',
])
def test_article_suppresses_stale_ineligible_or_absent_calculation(general,mutation):  # noqa: F811
    seed(general)
    with psycopg.connect(general.search_service.pool.conninfo) as conn:
        conn.execute(mutation)
    assert 'calculated_current' not in general.article(244565,'86')
    assert 'calculated_current' not in general.article(1,'1')
