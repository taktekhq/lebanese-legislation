"""Build a clean public database from a pinned published Parquet snapshot."""
import argparse
import json
import os
import re
import subprocess
import tempfile
from datetime import UTC, date, datetime
from hashlib import sha256
from pathlib import Path

import psycopg
from psycopg import sql
from psycopg.types.json import Jsonb

from .chunks import DIMENSIONS, GO_WHITESPACE, MODEL, chunks, reusable
from .sources import derive_documents, import_parquet, resource


def assert_empty_target(conn):
    occupied = conn.execute("""SELECT
      EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname !~ '^pg_')
      OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relkind IN ('r','v','m','p','S','f'))
      OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
        WHERE n.nspname='public' AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e'))
      OR EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
        WHERE n.nspname='public' AND t.typcategory<>'A' AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass AND d.objid=t.oid AND d.deptype='e'))
      OR EXISTS(SELECT 1 FROM pg_extension WHERE extname NOT IN ('plpgsql','vector'))
      OR EXISTS(SELECT 1 FROM pg_largeobject_metadata)
      OR EXISTS(SELECT 1 FROM pg_foreign_server)
      OR EXISTS(SELECT 1 FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()))
      """).fetchone()[0]
    if occupied:
        raise ValueError('refusing occupied target database')


def ensure_semantic(conn):
    conn.execute(resource('semantic.sql'))


def reuse_vectors(target, source, source_table='public.semantic_chunks'):
    """Stream saved vectors and retain only exact canonical public references.

    The production caller opens source with default_transaction_read_only=on.
    No private title, timestamps, run identifiers or other metadata is selected.
    """
    with target.cursor(name='public_documents_for_reuse') as public_rows:
        public_rows.execute('SELECT kind,source_id,part,lang,title,text,hash FROM source_documents')
        docs = {tuple(row[:4]): row[4:] for row in public_rows}
    inserted = 0
    query = sql.SQL('SELECT kind,source_id,part,lang,text,hash,chunk_start,chunk_end,embedding::text FROM {} WHERE model=%s').format(sql.Identifier(*source_table.split('.')))
    with source.cursor(name='public_vector_reuse') as rows, target.cursor() as writer:
        rows.itersize = 500
        rows.execute(query, (MODEL,))
        accepted = []
        insert = '''INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
                VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s) ON CONFLICT DO NOTHING'''
        for row in rows:
            doc = docs.get(tuple(row[:4]))
            if doc is None:
                continue
            title, text, hashed = doc
            excerpt, saved_hash, start, end, vector = row[4:]
            if saved_hash != hashed or not reusable(text, saved_hash, start, end, excerpt):
                continue
            accepted.append((MODEL, *row[:4], title, excerpt, hashed, start, end, vector))
            if len(accepted) == 500:
                writer.executemany(insert, accepted)
                inserted += writer.rowcount
                accepted = []
        if accepted:
            writer.executemany(insert, accepted)
            inserted += writer.rowcount
    return inserted


def _canonical_chunks(conn, checkpoint=False):
    # Generate canonical expected boundaries rather than counting unchecked rows.
    with conn.cursor(name='missing_documents', withhold=checkpoint) as rows:
        rows.execute('SELECT kind,source_id,part,lang,title,text,hash FROM source_documents ORDER BY kind,source_id,part,lang')
        for row in rows:
            saved = {r[0]: r[1:] for r in conn.execute('SELECT chunk_start,chunk_end,text,hash FROM semantic_chunks WHERE model=%s AND kind=%s AND source_id=%s AND part=%s AND lang=%s', (MODEL, *row[:4]))}
            for chunk in chunks(row[5]):
                yield row[:5], chunk, saved.get(chunk.start) == (chunk.end, chunk.text, chunk.hash)


def missing_chunks(conn, checkpoint=False):
    for ref, chunk, present in _canonical_chunks(conn, checkpoint=checkpoint):
        if not present:
            yield ref, chunk


def coverage(conn):
    expected = indexed = 0
    for _, _, present in _canonical_chunks(conn):
        expected += 1
        indexed += int(present)
    return {'expected': expected, 'indexed': indexed, 'missing': expected-indexed}


def fill_missing(conn, embedder, cap, checkpoint=False):
    """Fill within cap; checkpoint=True commits each successful provider batch."""
    missing = coverage(conn)['missing']
    if cap < 0 or missing > cap:
        raise ValueError(f'missing embeddings {missing} exceeds explicit cap {cap}')
    batch = []
    def save(items):
        vectors = embedder.embed([c.text for _, c in items], query=False)
        if len(vectors) != len(items):
            raise ValueError('embedding response count mismatch')
        for (ref, chunk), vector in zip(items, vectors):
            if len(vector) != DIMENSIONS:
                raise ValueError('embedding dimensions mismatch')
            encoded = '[' + ','.join(str(float(x)) for x in vector) + ']'
            conn.execute('''INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
                VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s) ON CONFLICT(model,kind,source_id,part,lang,hash,chunk_start)
                DO UPDATE SET title=excluded.title,text=excluded.text,chunk_end=excluded.chunk_end,embedding=excluded.embedding,updated_at=now()''',
                         (MODEL, *ref, chunk.text, chunk.hash, chunk.start, chunk.end, encoded))
    for ref, chunk in missing_chunks(conn, checkpoint=checkpoint):
        batch.append((ref, chunk))
        if len(batch) == 32:
            save(batch)
            if checkpoint:
                conn.commit()
            batch = []
    if batch:
        save(batch)
        if checkpoint:
            conn.commit()
    return missing


def validate_chunks(conn):
    invalid = conn.execute("""SELECT count(*) FROM semantic_chunks c LEFT JOIN source_documents d
      USING(kind,source_id,part,lang) WHERE d.hash IS NULL OR c.model<>%s OR c.hash<>d.hash
      OR d.hash<>encode(sha256(convert_to(d.text,'UTF8')),'hex')
      OR c.chunk_start%%780<>0 OR c.chunk_end<>least(c.chunk_start+900,length(d.text))
      OR (c.chunk_start<>0 AND c.chunk_start+120>=length(d.text))
      OR c.text<>substring(d.text FROM c.chunk_start+1 FOR c.chunk_end-c.chunk_start)
      OR c.title<>d.title OR btrim(d.text,%s)=''""", (MODEL, GO_WHITESPACE)).fetchone()[0]
    if invalid:
        raise ValueError(f'{invalid} chunks failed canonical validation')


def dump_database(dsn, destination, pg_dump='pg_dump'):
    # libpq receives credentials through the child environment, not process argv.
    from psycopg.conninfo import conninfo_to_dict
    env = os.environ.copy()
    mapping = {'host':'PGHOST','port':'PGPORT','user':'PGUSER','password':'PGPASSWORD','dbname':'PGDATABASE','sslmode':'PGSSLMODE'}
    for key, value in conninfo_to_dict(dsn).items():
        if key in mapping:
            env[mapping[key]] = value
    destination = Path(destination)
    if destination.exists():
        raise FileExistsError('refusing to overwrite existing dump')
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=destination.parent, suffix='.partial', delete=False) as stream:
            temporary = Path(stream.name)
            tables = re.findall(r'^CREATE TABLE ([a-z_]+)', resource('public_schema.sql'), re.MULTILINE)
            tables += ['source_documents','semantic_chunks','semantic_documents','lebleg_release']
            command = [pg_dump, '--format=custom', '--no-owner', '--no-acl', '--no-comments', '--extension=vector']
            command += ['--table=public.' + name for name in tables]
            subprocess.run(command, env=env, stdout=stream, check=True)
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary,destination)  # Atomic and refuses an unrelated existing destination.
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def export_release(dsn, out, manifest, pg_dump):
    dump = out / 'corpus.dump'
    dump_database(dsn, dump, pg_dump)
    h = sha256()
    with dump.open('rb') as stream:
        for data in iter(lambda:stream.read(1024*1024), b''):
            h.update(data)
    external = dict(manifest)
    external['dump'] = {'path':dump.name,'size_bytes':dump.stat().st_size,'sha256':h.hexdigest()}
    # A manifest is written only after a complete successful dump.
    (out / 'manifest.json').write_text(json.dumps(external, ensure_ascii=False, indent=2) + '\n')
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database-url', default=os.getenv('DATABASE_URL'))
    parser.add_argument('--snapshot-dir', required=True, help='Directory containing the pinned data/*.parquet files')
    parser.add_argument('--revision', required=True)
    parser.add_argument('--as-of-date',type=date.fromisoformat,default=date.today())
    parser.add_argument('--release-id', required=True)
    parser.add_argument('--output-dir', required=True)
    parser.add_argument('--repo-id', default='taktekhq/lebanese-legislation')
    parser.add_argument('--reuse-database-url', default=os.getenv('REUSE_DATABASE_URL'))
    parser.add_argument('--embed-missing', action='store_true')
    parser.add_argument('--max-new-embeddings', type=int, default=10000)
    parser.add_argument('--pg-dump', default=os.getenv('PG_DUMP_BIN', 'pg_dump'))
    parser.add_argument('--resume', action='store_true', help='Continue this exact local staging build after a coverage report')
    parser.add_argument('--dataset-license', default='other: public-texts-credit-the-centre; https://lebaneselegislation.com/data/')
    args = parser.parse_args(argv)
    if not args.database_url or not re.fullmatch('[0-9a-f]{40}', args.revision) or not re.fullmatch('[A-Za-z0-9][A-Za-z0-9._-]{0,127}', args.release_id):
        parser.error('database URL, immutable 40-hex revision and safe release ID required')
    out = Path(args.output_dir)
    out.mkdir(parents=True, exist_ok=True)
    dump = out / 'corpus.dump'
    if dump.exists() or (out / 'manifest.json').exists():
        raise ValueError('release artifacts already exist')
    with psycopg.connect(args.database_url) as conn:
        if not args.resume:
            assert_empty_target(conn)
        if conn.execute('SHOW server_version').fetchone()[0].split()[0] != '18.6':
            raise ValueError('build requires PostgreSQL 18.6')
        if args.resume and conn.execute("SELECT to_regclass('public.lebleg_release')").fetchone()[0]:
            finalized = conn.execute('SELECT manifest FROM lebleg_release WHERE release_id=%s',(args.release_id,)).fetchone()
            if (not finalized or finalized[0]['source']!={'repo_id':args.repo_id,'revision':args.revision}
                or finalized[0].get('as_of_date')!=args.as_of_date.isoformat()):
                raise ValueError('finalized release identity mismatch')
            manifest = finalized[0]
            for table in ('source_documents','semantic_chunks'):
                if conn.execute(sql.SQL('SELECT count(*) FROM {}').format(sql.Identifier(table))).fetchone()[0] != manifest['counts'][table]:
                    raise ValueError('finalized release count mismatch')
            validate_chunks(conn)
            return export_release(args.database_url,out,manifest,args.pg_dump)
        if args.resume:
            state = conn.execute("SELECT release_id,revision,tables,to_jsonb(s)->>'as_of_date' FROM lebleg_build_state s").fetchone()
            if not state or state[:2] != (args.release_id,args.revision):
                raise ValueError('staging release identity mismatch')
            if conn.execute("SELECT to_regclass('public.lebleg_release')").fetchone()[0]:
                raise ValueError('release already finalized')
            if state[3] is not None and state[3]!=args.as_of_date.isoformat():
                raise ValueError('staging as_of_date mismatch')
            tables = state[2]
            documents = conn.execute('SELECT count(*) FROM source_documents').fetchone()[0]
        else:
            tables = import_parquet(conn, args.snapshot_dir)
            documents = derive_documents(conn,args.as_of_date)
            ensure_semantic(conn)
            conn.execute('CREATE TABLE lebleg_build_state(release_id text,revision text,tables jsonb,as_of_date date)')
            conn.execute('INSERT INTO lebleg_build_state VALUES(%s,%s,%s,%s)',(args.release_id,args.revision,Jsonb(tables),args.as_of_date))
        if conn.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone()[0] != '0.8.2':
            raise ValueError('build requires pgvector 0.8.2')
        if args.reuse_database_url:
            with psycopg.connect(args.reuse_database_url, options='-c default_transaction_read_only=on') as source:
                reuse_vectors(conn, source)
        report = coverage(conn)
        (out / 'coverage.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report), flush=True)
        if report['missing'] and args.embed_missing:
            from .embedding import GeminiEmbedder
            conn.commit()
            fill_missing(conn, GeminiEmbedder(os.environ['GEMINI_API_KEY']), args.max_new_embeddings, checkpoint=True)
            report = coverage(conn)
        if report['missing']:
            # Useful staging output remains local. There is no readiness row or dump.
            conn.commit()
            return 2
        validate_chunks(conn)
        (out / 'coverage.json').write_text(json.dumps(report, indent=2) + '\n')
        conn.execute('DROP TABLE lebleg_build_state')
        conn.execute("INSERT INTO semantic_documents SELECT %s,kind,source_id,part,lang,hash,%s FROM source_documents", (MODEL, 'public-release:' + args.release_id))
        conn.execute('CREATE INDEX semantic_chunks_embedding ON semantic_chunks USING hnsw(embedding halfvec_cosine_ops)')
        generated = datetime.now(UTC).isoformat().replace('+00:00','Z')
        manifest = {'format_version':1,'release_id':args.release_id,'generated_at':generated,'as_of_date':args.as_of_date.isoformat(),
          'source':{'repo_id':args.repo_id,'revision':args.revision},
          'postgres':{'version':'18.6','vector_version':'0.8.2'},
          'embedding':{'model':MODEL,'dimensions':DIMENSIONS,'task_type':'RETRIEVAL_DOCUMENT','chunk_size':900,'chunk_overlap':120,'storage':'halfvec(768)'},
          'counts':{'tables':tables,'source_documents':documents,'semantic_chunks':report['indexed']},
          'dataset':{'license':args.dataset_license,'attribution':'Texts: Centre for Research and Studies in Legal Informatics, Lebanese University. Dataset structure: Taktek, LLC.'},'build_version':'0.1.0'}
        conn.execute('CREATE TABLE lebleg_release(release_id text PRIMARY KEY,manifest jsonb NOT NULL)')
        conn.execute('INSERT INTO lebleg_release VALUES(%s,%s)', (args.release_id,Jsonb(manifest)))
        conn.execute('ANALYZE')
    return export_release(args.database_url,out,manifest,args.pg_dump)
