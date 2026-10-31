"""Probe published API/MCP images against a verified, restricted snapshot reader."""

import json
import os
import re
import socket
import subprocess
import time
import uuid
from urllib.parse import quote

import httpx
import psycopg
from psycopg.conninfo import make_conninfo

from . import __version__


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        return sock.getsockname()[1]


def cleanup_smoke(owner):
    if not re.fullmatch('[0-9a-f]{64}',owner):
        raise ValueError('invalid smoke owner')
    listed=subprocess.run(['docker','ps','-aq','--filter','label=org.lebleg.pipeline.owner='+owner],
        capture_output=True,text=True,check=False)
    if listed.returncode:
        return
    for identifier in listed.stdout.splitlines():
        if not re.fullmatch('[0-9a-f]{12,64}',identifier):
            continue
        inspected=subprocess.run(['docker','inspect',identifier],capture_output=True,text=True,check=False)
        if inspected.returncode:
            continue
        item=json.loads(inspected.stdout)[0]
        if item.get('Name','').startswith('/lebleg-pipeline-smoke-') and item['Config'].get('Labels',{}).get('org.lebleg.pipeline.owner')==owner:
            subprocess.run(['docker','rm','-f',identifier],capture_output=True,check=False)


def main(argv=None):
    admin = os.environ['PIPELINE_VERIFY_DSN']
    reader = make_conninfo(admin, user=os.getenv('PIPELINE_READER_ROLE','lebleg_reader'),
                          password=os.environ['PIPELINE_READER_PASSWORD'])
    names = []
    try:
        with psycopg.connect(reader) as db:
            if db.execute("SELECT has_table_privilege(current_user,'legislation','INSERT,UPDATE,DELETE,TRUNCATE')").fetchone()[0]:
                raise ValueError('snapshot reader is writable')
            article = db.execute('SELECT law_id,number,text_ar FROM legislation_article WHERE law_id>0 ORDER BY law_id,position LIMIT 1').fetchone()
            ruling = db.execute('SELECT ruling_id FROM ruling_page WHERE ruling_id>0 ORDER BY ruling_id LIMIT 1').fetchone()
            gazette = db.execute('SELECT id FROM gazette_issue WHERE id>0 ORDER BY id LIMIT 1').fetchone()
        if not article:
            raise ValueError('snapshot has no article for resource checks')
        ports = {}
        for mode in ('api','mcp'):
            name = 'lebleg-pipeline-smoke-' + uuid.uuid4().hex[:12]
            names.append(name)
            ports[mode] = free_port()
            env = {**os.environ, 'DATABASE_URL':reader, 'APP_MODE':mode, 'GEMINI_MAX_CALLS_PER_DAY':'1'}
            image = os.getenv('PIPELINE_' + mode.upper() + '_IMAGE', 'ghcr.io/taktekhq/lebanese-legislation-' + mode + ':' + __version__)
            subprocess.run(['docker','run','-d','--name',name,'--network','host','--memory','256m','--read-only',
                '--tmpfs','/tmp','--label','org.lebleg.pipeline.owner='+os.getenv('PIPELINE_OWNER_ID',uuid.uuid4().hex*2),'--cap-drop','ALL','--security-opt','no-new-privileges:true',
                '-e','DATABASE_URL','-e','APP_MODE','-e','GEMINI_API_KEY','-e','GEMINI_MAX_CALLS_PER_DAY',
                image,'serve','--host','127.0.0.1','--port',str(ports[mode])], env=env,check=True,
                stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        urls = {kind:'http://127.0.0.1:' + str(port) for kind,port in ports.items()}
        with httpx.Client(timeout=45) as client:
            for url in urls.values():
                for _ in range(60):
                    try:
                        if client.get(url + '/healthz').status_code == 200:
                            break
                    except httpx.ConnectError:
                        pass
                    time.sleep(1)
                else:
                    raise ValueError('published image did not become healthy')
            def get(path, params=None):
                response = client.get(urls['api'] + path, params=params)
                response.raise_for_status()
                return response.json()
            def tool(name, arguments):
                response = client.post(urls['mcp'] + '/mcp', json={'jsonrpc':'2.0','id':1,'method':'tools/call',
                    'params':{'name':name,'arguments':arguments}})
                response.raise_for_status()
                result = response.json()['result']
                if result.get('isError'):
                    raise ValueError('MCP resource failed')
                return json.loads(result['content'][0]['text'])['result']
            law_id, number, text = article
            law = get(f'/api/v1/law/{law_id}/')
            if len(law['articles']) != law['article_count'] or tool('get_law',{'id':law_id}) != law:
                raise ValueError('API/MCP law parity failed')
            path = f"/api/v1/law/{law_id}/article/{quote('-'.join(number.split()), safe='')}/"
            resource = get(path)
            if resource.get('text_ar') != text or tool('get_article',{'law_id':law_id,'number':number}) != resource:
                raise ValueError('original article or MCP parity failed')
            if ruling and get(f'/api/v1/ruling/{ruling[0]}/') != tool('get_ruling',{'id':ruling[0]}):
                raise ValueError('ruling parity failed')
            if gazette and get(f'/api/v1/gazette/{gazette[0]}/') != tool('get_gazette_issue',{'id':gazette[0]}):
                raise ValueError('Gazette parity failed')
            query = (text or law['title'])[:35]
            search = get('/api/v1/search/',{'q':query,'type':'laws','semantic':'false','limit':3})
            if search != tool('search_legislation',{'query':query,'kind':'laws','semantic':False,'limit':3}):
                raise ValueError('API/MCP search parity failed')
            if client.get(urls['mcp'] + '/api/v1/laws/').status_code != 404:
                raise ValueError('MCP-only surface failed')
            if os.getenv('PIPELINE_SMOKE_SEMANTIC') == '1':
                from pathlib import Path

                from .budget import BudgetedEmbedder
                from .embedding import MODEL
                class Reservation:
                    model = MODEL
                # Persist at most three smoke-query attempts per release, including failures.
                budget = BudgetedEmbedder(Reservation(),3,Path(os.environ['PIPELINE_JOB_DIR']) / 'query-attempts.json')
                budget._reserve(1,1)
                result = get('/search',{'q':'What protections apply when an employee is dismissed without a valid reason in Lebanon?',
                                        'semantic':'true','limit':3})
                if result['semantic_status'] != 'complete' or not result['passages']:
                    raise ValueError('semantic image smoke failed')
        print(json.dumps({'ok':True,'status':'complete'}))
        return 0
    finally:
        for name in names:
            subprocess.run(['docker','rm','-f',name],check=False,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:  # noqa: BLE001 - smoke errors must never expose credentials
        print(json.dumps({'ok':False,'status':'failed'}))
        raise SystemExit(1) from None
