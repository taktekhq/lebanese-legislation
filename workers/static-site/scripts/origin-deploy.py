#!/usr/bin/env python3
"""Blue/green deployment state and checks, invoked by origin.yml from the repo root."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.request

# Cloudflare refuses the default Python-urllib user agent (403).
def _request(*args, **kwargs):
    req = urllib.request.Request(*args, **kwargs)
    req.add_header("User-Agent", UA)
    return req


UA = "lebleg-origin-deploy/1 (+https://github.com/taktekhq/lebanese-legislation)"
import urllib.error

CONFIG = Path('workers/origin/wrangler.toml')
STATE = Path('origin-state.json')
CHECKS = ('/healthz', '/api/v1/search/?limit=1&q=law', '/laws/?year=2019')


def marker(name, value=None):
    target = f's3://lebleg-site/_build/{name}'
    args = ['aws', 's3', 'cp', '--endpoint-url', os.environ['R2_ENDPOINT']]
    if value is not None:
        subprocess.run(args + ['-', target], input=value + '\n', text=True, check=True)
        return value
    res = subprocess.run(args + [target, '-'], capture_output=True, text=True)
    if res.returncode:
        if re.search(r'\(404\)|NoSuchKey|Not Found', res.stderr):
            return None
        raise RuntimeError(res.stderr)
    return res.stdout.strip()


def image_build(image, revision=''):
    """Use the pulled image's revision label, or an immutable full-commit tag."""
    if not revision:
        tag = image.rsplit('/', 1)[-1].split(':', 1)[-1]
        match = re.fullmatch(r'(?:source-)?([0-9a-f]{40})(?:-\d+-\d+)?', tag)
        revision = match[1] if match else ''
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError(f'Image {image} has no full git commit revision label or tag')
    return revision


def stage(image):
    live = marker('live-side') or 'blue'
    if live not in ('blue', 'green'):
        raise ValueError(f'Invalid live-side: {live}')
    if not image or any(c in image for c in ('\n', '\r')):
        raise ValueError('Missing or invalid image reference')
    idle = 'green' if live == 'blue' else 'blue'
    state = {'old': live, 'idle': idle, 'new_image': image,
             'expected_build': image_build(image, os.environ.get('EXPECTED_BUILD', ''))}
    text = CONFIG.read_text()
    pattern = rf'(\[\[env\.{idle}\.containers\]\]\nclass_name = "Origin"\nimage = )[^\n]+'
    text, count = re.subn(pattern, lambda m: m[1] + json.dumps(image), text)
    if count != 1:
        raise ValueError(f'Expected one container configuration for {idle}')
    CONFIG.write_text(text)
    STATE.write_text(json.dumps(state, indent=2) + '\n')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
        out.write(f'old={live}\nidle={idle}\n')
    return state


def deploy_origin(side):
    args = ['npx', '--yes', 'wrangler@4.151.1']
    subprocess.run(args + ['deploy', '--env', side], cwd='workers/origin', check=True)
    secrets = {key: os.environ[key] for key in ('GEMINI_API_KEY', 'LEBLEG_VECTORIZE_TOKEN')}
    subprocess.run(args + ['secret', 'bulk', '--env', side], input=json.dumps(secrets),
                   text=True, cwd='workers/origin', check=True)
    wait_rollout(side)


def wait_rollout(side, seconds=1800):
    # Cloudflare replaces a side's container instances after a deploy (a rollout).
    # Warming before it ends warms an instance that is about to be replaced.
    name = f'lebleg-origin-{side}-origin-{side}'
    args = ['npx', '--yes', 'wrangler@4.151.1', 'containers']
    deadline = time.time() + seconds
    while time.time() < deadline:
        apps = json.loads(subprocess.run(args + ['list', '--json'], cwd='workers/origin', check=True,
                                         capture_output=True, text=True).stdout)
        app = next((a for a in apps if a.get('name') == name), None)
        if app:
            info = json.loads(subprocess.run(args + ['info', app['id']], cwd='workers/origin', check=True,
                                             capture_output=True, text=True).stdout)
            starting = (info.get('health') or {}).get('instances', {}).get('starting', 0)
            if not info.get('active_rollout_id') and not starting:
                print(f'{name}: rollout finished')
                return
            print(f'{name}: rollout {info.get("active_rollout_id")}, starting {starting}')
        time.sleep(30)
    raise RuntimeError(f'{name}: rollout did not finish in {seconds} s')


def deploy_site_worker():
    args = ['npx', '--yes', 'wrangler@4.151.1', 'deploy', '--var', 'ORIGIN_MODE:' + os.environ['MODE']]
    for route in os.environ.get('ROUTES', '').split(','):
        if route.strip():
            args += ['--route', route.strip()]
    subprocess.run(args, cwd='workers/static-site', check=True)
    subprocess.run(['npx', '--yes', 'wrangler@4.151.1', 'secret', 'put', 'ORIGIN_TEST_KEY'],
                   input=os.environ['ORIGIN_TEST_KEY'] + '\n', text=True,
                   cwd='workers/static-site', check=True)


def check(seconds, expected_build, side=None):
    expected_build = image_build('', expected_build)
    seen_build = '<no response>'
    deadline = time.monotonic() + seconds
    headers = {'Cache-Control': 'no-cache'}
    if side:
        headers.update({'x-origin-test': os.environ['ORIGIN_TEST_KEY'], 'x-origin-side': side})
        if not headers['x-origin-test']:
            raise ValueError('ORIGIN_TEST_KEY is required')
    while time.monotonic() < deadline:
        ok = True
        for path in CHECKS:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                ok = False
                break
            try:
                req = _request('https://lebaneselegislation.com' + path, headers=headers)
                with urllib.request.urlopen(req, timeout=min(remaining, 30)) as res:
                    status = res.status
                    if path == '/healthz':
                        try:
                            body = json.load(res)
                            seen_build = body.get('build') if isinstance(body, dict) else None
                        except (ValueError, UnicodeError):
                            seen_build = '<invalid JSON>'
                        print(f'/healthz build seen: {seen_build!r}, expected: {expected_build}', flush=True)
                        if seen_build != expected_build:
                            ok = False
                print(f'{path}: {status}', flush=True)
                if status != 200:
                    ok = False
            except (urllib.error.URLError, TimeoutError) as err:
                if path == '/healthz':
                    seen_build = f'<unavailable: {err}>'
                print(f'{path}: {err}', flush=True)
                ok = False
        if ok and time.monotonic() <= deadline:
            return
        time.sleep(min(5, max(0, deadline - time.monotonic())))
    raise SystemExit(f'Origin checks did not pass within {seconds} seconds: '
                     f'/healthz build seen {seen_build!r}, expected {expected_build}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['site-worker', 'stage', 'switch', 'rollback', 'warm', 'verify', 'retire'])
    args = parser.parse_args()
    if args.action == 'site-worker':
        if not os.environ.get('ORIGIN_TEST_KEY'):
            raise ValueError('ORIGIN_TEST_KEY is required')
        deploy_site_worker()
        return
    if args.action == 'stage':
        if not os.environ.get('ORIGIN_TEST_KEY'):
            raise ValueError('ORIGIN_TEST_KEY is required before staging')
        for key in ('GEMINI_API_KEY', 'LEBLEG_VECTORIZE_TOKEN'):
            if not os.environ.get(key):
                raise ValueError(f'{key} is required before staging')
        state = stage(os.environ['IMAGE'])
        deploy_origin(state['idle'])
        return
    state = json.loads(STATE.read_text())
    if args.action in ('switch', 'rollback'):
        side = state['idle'] if args.action == 'switch' else state['old']
        marker('live-side', side)
        time.sleep(20)  # Site Worker isolates cache the flag for 15 seconds.
    elif args.action == 'warm':
        check(1800, state['expected_build'], state['idle'])
    elif args.action == 'verify':
        # A late restart of the new side shows up within minutes, not seconds:
        # check now, then twice more two minutes apart.
        for i in range(3):
            if i:
                time.sleep(120)
            check(60, state['expected_build'])
    elif args.action == 'retire':
        req = _request('https://lebaneselegislation.com/_origin/restart?side=' + state['old'],
                                     headers={'x-origin-test': os.environ['ORIGIN_TEST_KEY']})
        with urllib.request.urlopen(req, timeout=60) as res:
            if res.status != 200 or res.read() != b'container stopped\n':
                raise RuntimeError('Old container was not stopped')


if __name__ == '__main__':
    main()
