"""Executable release stages using the existing public builder and verifier."""

import hashlib
import json
import os
import shutil
import tempfile
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo

from .manifest import load_manifest, safe_path, verify_file
from .pipeline import run_adapter
from .pipeline_metadata import update_block
from .publish import _read, promote_candidate, upload_candidate, verify_candidate
from .restore import check_database, restore


def atomic_text(path, text):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.is_symlink():
        raise ValueError('refusing a symbolic link output')
    temporary = None
    try:
        with tempfile.NamedTemporaryFile('w', dir=path.parent, delete=False) as out:
            temporary = Path(out.name)
            out.write(text)
            out.flush()
            os.fsync(out.fileno())
        os.replace(temporary, path)
    finally:
        if temporary:
            temporary.unlink(missing_ok=True)


def finish_export(dsn, directory, manifest, pg_dump):
    from .build import export_release
    directory = Path(directory)
    path, dump = directory / 'manifest.json', directory / 'corpus.dump'
    if path.exists():
        existing = load_manifest(path)
        if existing['source'] != manifest['source'] or existing['release_id'] != manifest['release_id']:
            raise ValueError('existing export identity mismatch')
        verify_file(dump, existing['dump'])
        return existing
    if dump.exists():
        # dump_database links this name only after the complete pg_dump is fsynced.
        with dump.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        value = {**manifest, 'dump': {'path': dump.name, 'size_bytes': dump.stat().st_size, 'sha256': digest}}
        atomic_text(path, json.dumps(value, indent=2) + '\n')
    else:
        export_release(dsn, directory, manifest, pg_dump)
    return load_manifest(path)


def check_scan_counts(paths, counts):
    import pyarrow.parquet as pq
    for key in ('documents','pages'):
        if pq.read_metadata(paths[key]).num_rows != counts[key]:
            raise ValueError('scan index count mismatch')


def retired_jobs(current, jobs):
    previous = [j for j in jobs if j['repo'] == current['repo'] and j['id'] < current['id']
                and j['status'] == 'complete' and j['release_id'].startswith('auto-')]
    return previous[:-1]


class ReleaseSteps:
    def __init__(self, coordinator):
        self.co = coordinator
        self.config = coordinator.config
        self.hub = coordinator.hub
        self.token = os.getenv(self.config.get('hf_token_env', 'HF_TOKEN'))

    def directory(self, job):
        path = self.co.root / 'jobs' / job['release_id']
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        return path

    def context(self, job):
        return json.loads((self.directory(job) / 'context.json').read_text())

    def _dsn(self, job, create=False, verify=False):
        admin = os.getenv(self.config.get('admin_dsn_env', 'PIPELINE_ADMIN_DSN'))
        if not admin:
            raise ValueError('a dedicated staging administrator is required')
        name = ('llpv_' if verify else 'llpb_') + hashlib.sha256((job['repo'] + job['release_id']).encode()).hexdigest()[:24]
        identity = 'lebleg-pipeline-v1:' + job['revision'] + ':' + job['fingerprint']
        intent = self.directory(job) / (('verify' if verify else 'build') + '-database.json')
        with psycopg.connect(admin, autocommit=True) as db:
            row = db.execute('SELECT pg_get_userbyid(datdba),shobj_description(oid,\'pg_database\') FROM pg_database WHERE datname=%s', (name,)).fetchone()
            if not row and create:
                atomic_text(intent, json.dumps({'name': name, 'identity': identity}) + '\n')
                db.execute(sql.SQL('CREATE DATABASE {}').format(sql.Identifier(name)))
                db.execute(sql.SQL('COMMENT ON DATABASE {} IS {}').format(sql.Identifier(name), sql.Literal(identity)))
            elif row and row[1] is None and create and intent.exists() and json.loads(intent.read_text()) == {'name':name,'identity':identity}:
                from .build import assert_empty_target
                with psycopg.connect(make_conninfo(admin, dbname=name)) as empty:
                    assert_empty_target(empty)
                if row[0] != db.execute('SELECT current_user').fetchone()[0]:
                    raise ValueError('staging database owner mismatch')
                db.execute(sql.SQL('COMMENT ON DATABASE {} IS {}').format(sql.Identifier(name), sql.Literal(identity)))
            elif not row or row[1] != identity or row[0] != db.execute('SELECT current_user').fetchone()[0]:
                raise ValueError('staging database is absent or belongs to another job')
        return make_conninfo(admin, dbname=name)

    def _manifest(self, job):
        with psycopg.connect(self._dsn(job), options='-c default_transaction_read_only=on') as db:
            rows = db.execute('SELECT release_id,manifest FROM lebleg_release').fetchall()
        if len(rows) != 1 or rows[0][0] != job['release_id'] or rows[0][1]['source'] != {'repo_id':job['repo'], 'revision':job['revision']}:
            raise ValueError('staged release identity mismatch')
        return rows[0][1]

    def __call__(self, stage, job):
        source = self.co.source(job['repo'])
        if stage in source.get('stage_commands', {}):
            return run_adapter(source['stage_commands'][stage], self.environment(job), self.config.get('adapter_timeout', 43200))
        if source.get('kind') == 'scans':
            if stage == 'prepare':
                return self.prepare_scans(job)
            if stage == 'metadata':
                return self.metadata(job, scans=True)
            return {'ok':True, 'kind':'scans'}
        return getattr(self, stage)(job)

    def environment(self, job):
        return {'PIPELINE_REPO': job['repo'], 'PIPELINE_REVISION': job['revision'],
                'PIPELINE_FINGERPRINT': job['fingerprint'], 'PIPELINE_RELEASE_ID': job['release_id'],
                'PIPELINE_JOB_DIR': str(self.directory(job)), 'PIPELINE_OWNER_ID':hashlib.sha256(str(self.co.root).encode()).hexdigest()}

    def prepare(self, job):
        if shutil.disk_usage(self.co.root).free < self.config.get('minimum_free_bytes', 40 * 1024**3):
            raise OSError(28, 'insufficient release workspace')
        directory = self.directory(job)
        if not (directory / 'context.json').exists():
            head = self.hub.repo_info(job['repo'], repo_type='dataset').sha
            files = self.hub.list_repo_files(job['repo'], repo_type='dataset', revision=head)
            previous = json.loads(_read(self.hub, job['repo'], 'releases/latest.json', head)).get('release_id') if 'releases/latest.json' in files else None
            atomic_text(directory / 'context.json', json.dumps({'previous_release':previous,
                'as_of_date':datetime.now(ZoneInfo(self.config.get('timezone', 'Asia/Beirut'))).date().isoformat()}) + '\n')
        from huggingface_hub import snapshot_download
        snapshot_download(job['repo'], repo_type='dataset', revision=job['revision'],
                          allow_patterns=['data/*.parquet'], local_dir=directory / 'input', token=False)
        if self.co.fingerprint(job['repo'], job['revision']) != job['fingerprint']:
            raise ValueError('source content identity changed')
        self._dsn(job, create=True)
        return {'ok':True, 'fingerprint':job['fingerprint']}

    def index(self, job):
        from .build import main
        directory, dsn = self.directory(job), self._dsn(job)
        out = directory / 'release'
        out.mkdir(exist_ok=True)
        with psycopg.connect(dsn) as db:
            resumed = bool(db.execute("SELECT to_regclass('public.lebleg_build_state') IS NOT NULL OR to_regclass('public.lebleg_release') IS NOT NULL").fetchone()[0])
        args = ['--database-url', dsn, '--snapshot-dir', str(directory / 'input' / 'data'), '--revision',job['revision'],
                '--repo-id',job['repo'], '--release-id',job['release_id'], '--as-of-date',self.context(job)['as_of_date'],
                '--output-dir',str(out), '--embed-missing', '--no-export',
                '--max-new-embeddings',str(self.config.get('max_new_embeddings', 2000000)),
                '--daily-embedding-budget-dir',str(self.co.root / 'embedding-budget'),
                '--daily-embedding-usd',str(self.config.get('daily_embedding_usd', '1')),
                '--embedding-usd-per-million',str(self.config.get('embedding_usd_per_million', '0.15')),
                '--budget-timezone',self.config.get('timezone','Asia/Beirut')]
        reuse = os.getenv(self.config.get('reuse_dsn_env', 'PIPELINE_REUSE_DSN'))
        completed = [j for j in self.co.ledger.status()['jobs'] if j['repo'] == job['repo'] and j['status'] == 'complete' and j['release_id'].startswith('auto-')]
        if completed:
            reuse = self._dsn(completed[-1])
        if reuse:
            args += ['--reuse-database-url',reuse]
        if resumed:
            args += ['--resume']
        if main(args):
            raise ValueError('staging build incomplete')
        return {'ok':True, 'counts':self._manifest(job)['counts']}

    def validate(self, job):
        manifest = self._manifest(job)
        with psycopg.connect(self._dsn(job), options='-c default_transaction_read_only=on') as db:
            check_database(db, manifest)
        return {'ok':True, 'counts':manifest['counts']}

    def export(self, job):
        directory = self.directory(job) / 'release'
        manifest = finish_export(self._dsn(job), directory, self._manifest(job), self.config.get('pg_dump', 'pg_dump'))
        dsn = self._dsn(job, create=True, verify=True)
        restore(manifest, directory / manifest['dump']['path'], dsn,
                self.config.get('reader_role', 'lebleg_reader'), self.config.get('pg_restore', 'pg_restore'))
        return {'ok':True, 'digest':manifest['dump']['sha256']}

    def upload(self, job):
        revision = upload_candidate(self.directory(job) / 'release', job['repo'], self.token)
        return {'ok':True, 'artifact_revision':revision}

    def check(self, job):
        directory = self.directory(job) / 'release'
        manifest = load_manifest(directory / 'manifest.json')
        revision = self.co.ledger.receipt(job['id'], 'upload')['artifact_revision']
        proof = verify_candidate(job['repo'], revision, manifest, local_dump=directory / manifest['dump']['path'])
        command = self.config.get('smoke_command')
        if not command:
            raise ValueError('published image smoke command is required')
        env = self.environment(job)
        env['PIPELINE_VERIFY_DSN'] = self._dsn(job, verify=True)
        from .pipeline_smoke import cleanup_smoke
        cleanup_smoke(env['PIPELINE_OWNER_ID'])
        try:
            run_adapter(command, env, self.config.get('smoke_timeout', 600))
        finally:
            cleanup_smoke(env['PIPELINE_OWNER_ID'])
        return proof

    def promote(self, job):
        manifest = load_manifest(self.directory(job) / 'release' / 'manifest.json')
        revision = self.co.ledger.receipt(job['id'], 'upload')['artifact_revision']
        head = promote_candidate(job['repo'], revision, manifest, self.context(job)['previous_release'], self.token)
        return {'ok':True, 'artifact_revision':head, 'release_id':manifest['release_id']}

    def prepare_scans(self, job):
        import re
        revision, repo = job['revision'], job['repo']
        raw = _read(self.hub, repo, 'MANIFEST.sha256', revision).decode()
        if len(raw) > 1024 * 1024:
            raise ValueError('scan manifest too large')
        expected = {}
        for line in raw.splitlines():
            digest, path = line.split('  ', 1)
            safe_path(path)
            if not re.fullmatch('[0-9a-f]{64}', digest) or path in expected:
                raise ValueError('invalid scan manifest')
            expected[path] = digest
        from .pipeline_state import _DATA
        files = list(self.hub.list_repo_tree(repo, repo_type='dataset', revision=revision, recursive=True))
        checked = 0
        for item in files:
            if not hasattr(item, 'size') or not _DATA.fullmatch(item.path):
                continue
            if item.path not in expected:
                raise ValueError('scan content missing from manifest')
            actual = item.lfs.sha256 if item.lfs else hashlib.sha256(_read(self.hub, repo, item.path, revision)).hexdigest()
            if actual != expected[item.path]:
                raise ValueError('scan manifest identity mismatch')
            checked += 1
        if not checked or 'stats.json' not in expected:
            raise ValueError('scan metadata incomplete')
        stats = _read(self.hub, repo, 'stats.json', revision)
        if hashlib.sha256(stats).hexdigest() != expected['stats.json']:
            raise ValueError('scan statistics mismatch')
        values = json.loads(stats)
        counts = {k:values[k] for k in ('documents','pages')}
        if any(type(v) is not int or v < 0 for v in counts.values()):
            raise ValueError('invalid scan counts')
        from huggingface_hub import hf_hub_download
        indexes = {}
        by_path = {item.path:item for item in files if hasattr(item,'size')}
        for key in ('documents','pages'):
            name = 'data/' + key + '.parquet'
            if name not in expected or name not in by_path:
                raise ValueError('scan index missing from manifest')
            path = hf_hub_download(repo, name, repo_type='dataset', revision=revision, token=False)
            verify_file(path, {'size_bytes':by_path[name].size,'sha256':expected[name]})
            indexes[key] = path
        check_scan_counts(indexes,counts)
        atomic_text(self.directory(job) / 'scan-counts.json', json.dumps(counts) + '\n')
        return {'ok':True, 'counts':counts}

    @staticmethod
    def write_overlay(source, body):
        if source.get('card_block_file'):
            atomic_text(source['card_block_file'], body.strip() + '\n')

    def drop_owned_database(self, job, verify=False):
        admin = os.environ[self.config.get('admin_dsn_env', 'PIPELINE_ADMIN_DSN')]
        name = ('llpv_' if verify else 'llpb_') + hashlib.sha256((job['repo'] + job['release_id']).encode()).hexdigest()[:24]
        identity = 'lebleg-pipeline-v1:' + job['revision'] + ':' + job['fingerprint']
        with psycopg.connect(admin, autocommit=True) as db:
            row = db.execute("SELECT shobj_description(oid,'pg_database'),datdba=current_user::regrole::oid FROM pg_database WHERE datname=%s", (name,)).fetchone()
            if not row:
                return True
            if row != (identity, True):
                raise ValueError('refusing cleanup of an unowned database')
            if db.execute('SELECT 1 FROM pg_stat_activity WHERE datname=%s LIMIT 1', (name,)).fetchone():
                return False
            db.execute(sql.SQL('DROP DATABASE {}').format(sql.Identifier(name)))
        return True

    def cleanup(self, job):
        self.drop_owned_database(job, verify=True)
        for old in retired_jobs(job, self.co.ledger.status()['jobs']):
            if self.drop_owned_database(old) and self.drop_owned_database(old, verify=True):
                path = self.co.root / 'jobs' / old['release_id']
                if path.exists():
                    if path.is_symlink() or path.resolve().parent != (self.co.root / 'jobs').resolve():
                        raise ValueError('invalid owned workspace')
                    shutil.rmtree(path)

    def metadata(self, job, scans=False):
        from huggingface_hub import CommitOperationAdd, HfApi
        source = self.co.source(job['repo'])
        if scans:
            counts = json.loads((self.directory(job) / 'scan-counts.json').read_text())
            body = f"{counts['documents']:,} source PDFs and {counts['pages']:,} pages. Mixed print and handwriting; transcription labels remain separate."
        else:
            manifest = load_manifest(self.directory(job) / 'release' / 'manifest.json')
            counts = manifest['counts']
            base = f"https://huggingface.co/datasets/{job['repo']}"
            body = f"**PostgreSQL + pgvector:** [Download the database dump]({base}/resolve/main/releases/{job['release_id']}/{manifest['dump']['path']}?download=true). {counts['source_documents']:,} source records and {counts['semantic_chunks']:,} indexed passages."
            if self.config.get('setup_url'):
                body += f" [Run with Docker]({self.config['setup_url']})."
        api = HfApi(token=self.token)
        head = api.repo_info(job['repo'], repo_type='dataset').sha
        original = _read(api, job['repo'], 'README.md', head).decode()
        updated = update_block(original, body)
        if updated != original:
            api.create_commit(job['repo'], repo_type='dataset', parent_commit=head,
                              operations=[CommitOperationAdd(path_in_repo='README.md',path_or_fileobj=updated.encode())],
                              commit_message='Update verified release metadata')
        self.write_overlay(source, body)
        if source.get('collection'):
            api.update_collection_item(source['collection'], source['collection_item_id'], note=body[:500])
        if source.get('metadata_command'):
            run_adapter(source['metadata_command'], self.environment(job), self.config.get('adapter_timeout',3600))
        if not scans:
            self.cleanup(job)
        return {'ok':True, 'counts':counts}
