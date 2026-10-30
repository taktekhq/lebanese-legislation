"""Pinned-source reconciliation. Runtime settings and credentials stay outside Git."""

import argparse
import json
import os
import signal
import subprocess
from datetime import datetime, timedelta
from pathlib import Path
from zoneinfo import ZoneInfo

from .budget import BudgetError
from .pipeline_state import Ledger, content_fingerprint, worker_lock


def load_config(path):
    config = json.loads(Path(path).read_text())
    if config.get('format_version', 1) != 1 or not config.get('state_dir') or not isinstance(config.get('sources'), list):
        raise ValueError('invalid pipeline configuration')
    if config.get('env_file'):
        source = Path(config['env_file'])
        if source.is_symlink() or source.stat().st_mode & 0o077:
            raise ValueError('environment file must be a restricted regular file')
        for line in source.read_text().splitlines():
            if not line.strip() or line.lstrip().startswith('#'):
                continue
            key, value = line.split('=', 1)
            if not key.isidentifier():
                raise ValueError('invalid environment variable')
            os.environ[key] = value
    return config


def run_adapter(command, environment, timeout=3600):
    if not isinstance(command, list) or not command or any(not isinstance(v, str) for v in command):
        raise ValueError('adapter must be an argument list')
    process = subprocess.Popen(command, env={**os.environ, **environment}, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        output, _ = process.communicate(timeout=timeout)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
        raise ValueError('adapter did not complete') from None
    if process.returncode or len(output) > 65536:
        raise ValueError('adapter failed')
    try:
        value = json.loads(output)
        if not isinstance(value, dict):
            raise TypeError()
        # Deliberately small protocol. Never persist arbitrary stderr, env or DSNs.
        allowed = {'ok', 'repo', 'revision', 'fingerprint', 'release_id', 'count', 'counts', 'digest',
                   'stage', 'kind', 'status', 'artifact_revision', 'public'}
        if set(value) - allowed or value.get('ok') is False:
            raise ValueError()
        json.dumps(value, allow_nan=False)
        return value
    except (ValueError, TypeError):
        raise ValueError('invalid adapter receipt') from None


class Coordinator:
    def __init__(self, config, hub=None):
        self.config = config
        self.root = Path(config['state_dir']).resolve()
        self.root.mkdir(parents=True, exist_ok=True)
        self.root.chmod(0o700)
        self.ledger = Ledger(self.root / 'state.sqlite')
        if hub is None:
            from huggingface_hub import HfApi
            hub = HfApi(token=False)
        self.hub = hub

    def source(self, repo):
        return next(source for source in self.config['sources'] if source['repo'] == repo)

    def fingerprint(self, repo, revision):
        files = []
        for item in self.hub.list_repo_tree(repo, repo_type='dataset', revision=revision, recursive=True):
            if not hasattr(item, 'size'):
                continue
            lfs = getattr(item, 'lfs', None)
            files.append({'path': item.path, 'oid': getattr(item, 'blob_id', None), 'size': item.size,
                          'lfs': {'sha256': lfs.sha256} if lfs else None})
        return content_fingerprint(files)

    def detect(self):
        count = 0
        for source in self.config['sources']:
            repo = source['repo']
            revision = self.hub.repo_info(repo, repo_type='dataset').sha
            fingerprint = self.fingerprint(repo, revision)
            # Scan changes can update verified metadata, but never create corpus text.
            if self.ledger.observe(repo, revision, fingerprint, approved=source.get('kind') == 'scans'):
                count += 1
        return count

    def approve(self, repo, revision, fingerprint):
        self.source(repo)
        if self.fingerprint(repo, revision) != fingerprint:
            raise ValueError('approved source fingerprint mismatch')
        head = self.hub.repo_info(repo, repo_type='dataset').sha
        if self.fingerprint(repo, head) != fingerprint:
            raise ValueError('approved content is no longer current')
        return self.ledger.observe(repo, revision, fingerprint, approved=True)

    def seed(self, repo, revision, release_id):
        self.source(repo)
        self.ledger.seed(repo, revision, self.fingerprint(repo, revision), release_id)

    def run(self, handler=None):
        with worker_lock(self.root / 'worker.lock') as held:
            if not held:
                return False
            job = self.ledger.next_job()
            if not job:
                return True
            if handler is None:
                from .pipeline_steps import ReleaseSteps
                handler = ReleaseSteps(self)
            while job['stage'] != 'complete':
                stage = job['stage']
                try:
                    receipt = handler(stage, job)
                    self.ledger.complete_stage(job['id'], stage, receipt)
                except Exception as error:  # noqa: BLE001 - never persist credential-bearing exception text
                    code, delay = 'stage_failed', None
                    if isinstance(error, BudgetError):
                        now = datetime.now(ZoneInfo(self.config.get('timezone', 'Asia/Beirut')))
                        tomorrow = (now + timedelta(days=1)).replace(hour=0, minute=0, second=5, microsecond=0)
                        code, delay = 'budget_exhausted', max(5, tomorrow.timestamp() - now.timestamp())
                    elif isinstance(error, PermissionError):
                        code = 'permission_denied'
                    elif isinstance(error, OSError) and getattr(error, 'errno', None) == 28:
                        code = 'disk_space'
                    self.ledger.fail(job['id'], code, retry_after=delay)
                    return False
                job = self.ledger.job(job['id'])
            return True


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('action', choices=['detect', 'approve', 'seed', 'run', 'tick', 'status', 'lane', 'retry'])
    parser.add_argument('--repo')
    parser.add_argument('--revision')
    parser.add_argument('--fingerprint')
    parser.add_argument('--release-id')
    parser.add_argument('--name')
    parser.add_argument('--job', type=int)
    parser.add_argument('--failed', action='store_true')
    args = parser.parse_args(argv)
    try:
        co = Coordinator(load_config(args.config))
        if args.action in ('detect', 'tick'):
            co.detect()
        if args.action in ('run', 'tick'):
            success = co.run()
            print(json.dumps({'ok': success}))
            return 0 if success else 1
        if args.action == 'approve':
            co.approve(args.repo, args.revision, args.fingerprint)
        elif args.action == 'seed':
            co.seed(args.repo, args.revision, args.release_id)
        elif args.action == 'lane':
            co.ledger.lane(args.name, args.repo, args.fingerprint, ok=not args.failed)
        elif args.action == 'retry':
            co.ledger.retry(args.job)
        if args.action != 'tick':
            print(json.dumps(co.ledger.status(), sort_keys=True))
        return 0
    except Exception:  # noqa: BLE001 - configuration failures must not disclose credentials
        print(json.dumps({'ok': False, 'error': 'pipeline_configuration_or_source_error'}))
        return 1
