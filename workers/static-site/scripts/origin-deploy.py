#!/usr/bin/env python3
"""Blue/green deployment state and checks, invoked by origin.yml from the repo root."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.parse
import urllib.request
import urllib.error

CONFIG = Path('workers/static-site/wrangler.toml')
STATE = Path('origin-state.json')
CHECKS = ('/healthz', '/api/v1/search/?limit=1&q=law', '/laws/?year=2019')


def api(path):
    req = urllib.request.Request(
        f'https://api.cloudflare.com/client/v4/accounts/{os.environ["CLOUDFLARE_ACCOUNT_ID"]}/{path}',
        headers={'Authorization': 'Bearer ' + os.environ['CLOUDFLARE_API_TOKEN']})
    with urllib.request.urlopen(req, timeout=60) as res:
        data = json.load(res)
    if not data['success']:
        raise RuntimeError(data.get('errors'))
    return data


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


def stage(image):
    bindings = api('workers/scripts/lebleg-static-site/settings')['result']['bindings']
    live = next((b['text'] for b in bindings if b['name'] == 'LIVE'), 'blue')
    if live not in ('blue', 'green'):
        raise ValueError(f'Invalid deployed LIVE: {live}')
    saved_side = marker('live-side')
    old_image = marker('live-image') if saved_side == live else None
    if not old_image:
        # Bootstrap from the existing Origin class before the first blue/green deploy.
        binding = next((b for b in bindings if b['name'] == 'ORIGIN_' + live.upper()), None)
        if binding is None:
            binding = next(b for b in bindings if b['name'] == 'ORIGIN')
        apps = []
        page = ''
        while True:
            data = api('containers/applications' + ('?page_token=' + urllib.parse.quote(page) if page else ''))
            apps.extend(data['result'])
            page = data.get('result_info', {}).get('next_page_token')
            if not page:
                break
        app = next(a for a in apps if a.get('durable_objects', {}).get('namespace_id') == binding['namespace_id'])
        old_image = app['configuration']['image']
    if not old_image or '\n' in old_image or '\n' in image:
        raise ValueError('Missing or invalid image reference')
    idle = 'green' if live == 'blue' else 'blue'
    state = {'old': live, 'idle': idle, 'old_image': old_image, 'new_image': image,
             'images': {live: old_image, idle: image}}
    STATE.write_text(json.dumps(state, indent=2) + '\n')
    marker('live-side', live)
    marker('live-image', old_image)
    configure(state, live)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
        out.write(f'old={live}\nidle={idle}\n')


def configure(state, live):
    text = CONFIG.read_text()
    text = re.sub(r'LIVE = "(?:blue|green)"', f'LIVE = "{live}"', text)
    for side, image in state['images'].items():
        pattern = rf'(class_name = "Origin{side.title()}"\nimage = )[^\n]+'
        text, count = re.subn(pattern, lambda m: m[1] + json.dumps(image), text)
        if count != 1:
            raise ValueError(f'Expected one container configuration for {side}')
    CONFIG.write_text(text)


def deploy():
    args = ['npx', '--yes', 'wrangler@4.151.1', 'deploy', '--var', 'ORIGIN_MODE:' + os.environ['MODE']]
    for route in os.environ.get('ROUTES', '').split(','):
        if route.strip():
            args += ['--route', route.strip()]
    subprocess.run(args, cwd='workers/static-site', check=True)


def check(seconds, side=None):
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
                req = urllib.request.Request('https://lebaneselegislation.com' + path, headers=headers)
                with urllib.request.urlopen(req, timeout=min(remaining, 30)) as res:
                    status = res.status
                print(f'{path}: {status}', flush=True)
                if status != 200:
                    ok = False
            except (urllib.error.URLError, TimeoutError) as err:
                print(f'{path}: {err}', flush=True)
                ok = False
        if ok and time.monotonic() <= deadline:
            return
        time.sleep(min(5, max(0, deadline - time.monotonic())))
    raise SystemExit(f'Origin checks did not pass within {seconds} seconds')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['stage', 'switch', 'rollback', 'warm', 'verify', 'retire'])
    args = parser.parse_args()
    if args.action == 'stage':
        if not os.environ.get('ORIGIN_TEST_KEY'):
            raise ValueError('ORIGIN_TEST_KEY is required before staging')
        stage(os.environ['IMAGE'])
        deploy()
        return
    state = json.loads(STATE.read_text())
    if args.action in ('switch', 'rollback'):
        side = state['idle'] if args.action == 'switch' else state['old']
        configure(state, side)
        deploy()
        marker('live-side', side)
        marker('live-image', state['images'][side])
    elif args.action == 'warm':
        check(1800, state['idle'])
    elif args.action == 'verify':
        check(60)
    elif args.action == 'retire':
        req = urllib.request.Request('https://lebaneselegislation.com/_origin/restart?side=' + state['old'],
                                     headers={'x-origin-test': os.environ['ORIGIN_TEST_KEY']})
        with urllib.request.urlopen(req, timeout=60) as res:
            if res.status != 200 or res.read() != b'container stopped\n':
                raise RuntimeError('Old container was not stopped')


if __name__ == '__main__':
    main()
