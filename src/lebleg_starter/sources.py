"""Import the public Parquet contract and derive eligible canonical documents."""
from importlib.resources import files
from pathlib import Path
from urllib.parse import quote

from psycopg import sql
from psycopg.types.json import Jsonb

from .chunks import digest


def resource(name):
    return files('lebleg_starter').joinpath('sql', name).read_text()


def source_details(kind, source_id, part, lang, fields):
    source_type = fields.get('kind') or kind
    route = kind
    if kind in ('law', 'article'):
        route = ('decree' if source_type.startswith('مرسوم') else 'law'
                 if source_type.startswith('قانون') or source_type in ('الدستور', 'دستور') else 'text')
    if kind == 'doctrine':
        route = 'doctrine'
    url = f'https://lebaneselegislation.com/{route}/{source_id}/'
    if kind == 'article':
        url += 'article/' + quote(part, safe='') + '/'
    metadata = {'source_type': source_type, 'language': lang, 'english_unofficial': lang == 'en',
                'authoritative_language': 'ar'}
    for key in ('number', 'issued_on', 'date', 'year', 'low_confidence', 'source_url', 'source', 'confidence'):
        if fields.get(key) is not None:
            metadata['identifier' if key == 'number' else key] = str(fields[key]) if key in ('issued_on', 'date') else fields[key]
    return url, metadata


def import_parquet(conn, directory):
    """Create all 29 typed snapshot tables, COPY batches, then public constraints."""
    import pyarrow.parquet as pq
    schema = resource('public_schema.sql')
    ddl, constraints = schema.split('ALTER TABLE', 1)
    conn.execute(ddl)
    expected = {r[0] for r in conn.execute("SELECT tablename FROM pg_tables WHERE schemaname='public'")}
    paths = {p.stem: p for p in Path(directory).glob('*.parquet')}
    if set(paths) != expected:
        raise ValueError('snapshot table inventory differs from public contract')
    counts = {}
    for name, path in sorted(paths.items()):
        pf = pq.ParquetFile(path)
        declared = [r[0] for r in conn.execute("SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=%s ORDER BY ordinal_position", (name,))]
        if pf.schema_arrow.names != declared:
            raise ValueError(f'{name}: column contract differs')
        with conn.cursor().copy(sql.SQL('COPY {} ({}) FROM STDIN').format(sql.Identifier(name), sql.SQL(',').join(map(sql.Identifier, declared)))) as cp:
            for batch in pf.iter_batches(batch_size=2000):
                for row in batch.to_pylist():
                    cp.write_row(tuple(row[c] for c in declared))
        counts[name] = pf.metadata.num_rows
    statements = ('ALTER TABLE' + constraints).split(';')
    for foreign in (False, True):
        for statement in statements:
            if statement.strip() and ('FOREIGN KEY' in statement) == foreign:
                conn.execute(statement)
    return counts


def derive_documents(conn):
    conn.execute('''CREATE TABLE source_documents (
      kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
      title text NOT NULL,text text NOT NULL,hash text NOT NULL,source_url text NOT NULL,metadata jsonb NOT NULL,
      PRIMARY KEY(kind,source_id,part,lang))''')
    query = resource('corpus.sql')
    query = ("SELECT c.*,coalesce(to_jsonb(l),to_jsonb(rp),to_jsonb(r),to_jsonb(o),to_jsonb(a),to_jsonb(rs),to_jsonb(d),'{}'::jsonb) FROM (" + query + """) c
      LEFT JOIN legislation l ON c.kind IN ('law','article') AND l.id=c.source_id
      LEFT JOIN ruling r ON c.kind='ruling' AND r.id=c.source_id
      LEFT JOIN ruling_page rp ON c.kind='ruling' AND rp.ruling_id=c.source_id AND rp.n::text=c.part
      LEFT JOIN opinion o ON c.kind='opinion' AND o.id=c.source_id
      LEFT JOIN agreement a ON c.kind='agreement' AND a.id=c.source_id
      LEFT JOIN research rs ON c.kind='research' AND rs.id=c.source_id
      LEFT JOIN doctrine_article d ON c.kind='doctrine' AND d.id=c.source_id
      WHERE btrim(coalesce(c.body,''))<>''""")
    with conn.cursor(name='canonical_documents') as rows, conn.cursor() as writer:
        rows.execute(query)
        for kind, sid, part, lang, title, text, fields in rows:
            url, metadata = source_details(kind, sid, part, lang, fields)
            writer.execute('INSERT INTO source_documents VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s)',
                           (kind, sid, part, lang, title or '', text, digest(text), url, Jsonb(metadata)))
    return conn.execute('SELECT count(*) FROM source_documents').fetchone()[0]
