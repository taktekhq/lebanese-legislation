"""Import the public Parquet contract and derive eligible canonical documents."""
import json
from datetime import date
from importlib.resources import files
from pathlib import Path
from urllib.parse import quote

from psycopg import sql
from psycopg.types.json import Jsonb

from .chunks import GO_WHITESPACE, digest


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
        spaced = part.translate(str.maketrans({char:' ' for char in GO_WHITESPACE}))
        slug = '-'.join(word for word in spaced.split(' ') if word)
        url += 'article/' + quote(slug, safe='') + '/'
    metadata = {'source_type': source_type, 'language': lang, 'english_unofficial': lang == 'en',
                'authoritative_language': 'ar'}
    for key in ('number', 'issued_on', 'decided_on', 'signed_on', 'published_on', 'date', 'year', 'low_confidence', 'source_url', 'source', 'confidence', 'model', 'read_at'):
        if fields.get(key) is not None:
            metadata['identifier' if key == 'number' else key] = str(fields[key]) if key in ('issued_on', 'decided_on', 'signed_on', 'published_on', 'date', 'read_at') else fields[key]
    if 'date' not in metadata:
        for key in ('issued_on','decided_on','signed_on','published_on'):
            if key in metadata:
                metadata['date']=metadata[key]
                break
    return url, metadata


def validate_arrow_type(arrow_type, pg_type):
    import pyarrow as pa
    expected = {'text':pa.string(),'jsonb':pa.string(),'int2':pa.int16(),'int4':pa.int32(),'int8':pa.int64(),
                'float4':pa.float32(),'date':pa.date32(),'timestamptz':pa.timestamp('us',tz='UTC'),
                'bool':pa.bool_(),'_text':pa.list_(pa.string()),'_int4':pa.list_(pa.int32())}
    if pg_type not in expected or arrow_type != expected[pg_type]:
        raise ValueError(f'Arrow type {arrow_type} differs from PostgreSQL public type {pg_type}')


def import_parquet(conn, directory, as_of_date=None):
    """Create typed snapshot tables, COPY batches, then public constraints."""
    import pyarrow.parquet as pq
    schema = resource('public_schema.sql')
    ddl, constraints = schema.split('ALTER TABLE', 1)
    conn.execute(ddl)
    expected = {r[0] for r in conn.execute("SELECT tablename FROM pg_tables WHERE schemaname='public'")}
    paths = {p.stem: p for p in Path(directory).glob('*.parquet')}
    optional = {'article_calculated_current'}
    if set(paths) - expected or expected - set(paths) - optional:
        raise ValueError('snapshot table inventory differs from public contract')
    counts = {}
    # Validate every file before writing any source row. No implicit narrowing.
    parquet_files = {}
    for name, path in sorted(paths.items()):
        pf = pq.ParquetFile(path)
        declared_types = conn.execute("SELECT column_name,udt_name FROM information_schema.columns WHERE table_schema='public' AND table_name=%s ORDER BY ordinal_position", (name,)).fetchall()
        declared = [r[0] for r in declared_types]
        if pf.schema_arrow.names != declared:
            raise ValueError(f'{name}: column contract differs')
        for field, (_, pg_type) in zip(pf.schema_arrow, declared_types):
            validate_arrow_type(field.type, pg_type)
        parquet_files[name] = (pf,declared)
    for name,(pf,declared) in parquet_files.items():
        with conn.cursor().copy(sql.SQL('COPY {} ({}) FROM STDIN').format(sql.Identifier(name), sql.SQL(',').join(map(sql.Identifier, declared)))) as cp:
            for batch in pf.iter_batches(batch_size=2000):
                for row in batch.to_pylist():
                    if name == 'article_calculated_current':
                        try:
                            row['calculated_current'] = Jsonb(json.loads(row['calculated_current']))
                        except (TypeError, ValueError) as error:
                            raise ValueError('invalid calculated_current JSON') from error
                    cp.write_row(tuple(row[c] for c in declared))
        counts[name] = pf.metadata.num_rows
    counts.setdefault('article_calculated_current', 0)
    validate_calculated_rows(conn, as_of_date or date.today())
    statements = ('ALTER TABLE' + constraints).split(';')
    for foreign in (False, True):
        for statement in statements:
            if statement.strip() and ('FOREIGN KEY' in statement) == foreign:
                conn.execute(statement)
    return counts


def derive_documents(conn, as_of_date=None):
    as_of_date = as_of_date or date.today()
    conn.execute("SELECT set_config('lebleg.as_of_date',%s,true)",(as_of_date.isoformat(),))
    conn.execute('''CREATE TABLE source_documents (
      kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
      title text NOT NULL,text text NOT NULL,hash text NOT NULL,source_url text NOT NULL,metadata jsonb NOT NULL,
      PRIMARY KEY(kind,source_id,part,lang))''')
    query = resource('corpus.sql').replace('CURRENT_DATE', "current_setting('lebleg.as_of_date')::date")
    query = ("""SELECT c.*,CASE WHEN l.id IS NOT NULL THEN jsonb_build_object('kind',l.kind,'number',l.number,'issued_on',l.issued_on)
      WHEN r.id IS NOT NULL THEN jsonb_build_object('number',r.number,'year',r.year,'decided_on',r.decided_on,
        'model',rp.model,'read_at',rp.read_at,'low_confidence',rp.low_confidence)
      WHEN o.id IS NOT NULL THEN jsonb_build_object('number',o.number,'year',o.year,'issued_on',o.issued_on)
      WHEN a.id IS NOT NULL THEN jsonb_build_object('signed_on',a.signed_on)
      WHEN rs.id IS NOT NULL THEN jsonb_build_object('published_on',rs.published_on)
      ELSE '{}'::jsonb END FROM (""" + query + """) c
      LEFT JOIN legislation l ON c.kind IN ('law','article') AND l.id=c.source_id
      LEFT JOIN ruling r ON c.kind='ruling' AND r.id=c.source_id
      LEFT JOIN ruling_page rp ON c.kind='ruling' AND rp.ruling_id=c.source_id AND rp.n::text=c.part
      LEFT JOIN opinion o ON c.kind='opinion' AND o.id=c.source_id
      LEFT JOIN agreement a ON c.kind='agreement' AND a.id=c.source_id
      LEFT JOIN research rs ON c.kind='research' AND rs.id=c.source_id
      WHERE btrim(coalesce(c.body,''))<>''""")
    with conn.cursor(name='canonical_documents') as rows:
        rows.execute(query)
        while batch := rows.fetchmany(2000):
            with conn.cursor().copy('COPY source_documents FROM STDIN') as writer:
                for kind, sid, part, lang, title, text, fields in batch:
                    url, metadata = source_details(kind, sid, part, lang, fields)
                    writer.write_row((kind, sid, part, lang, title or '', text, digest(text), url, Jsonb(metadata)))
    refresh_metadata(conn)
    refresh_calculated_metadata(conn, as_of_date)
    return conn.execute('SELECT count(*) FROM source_documents').fetchone()[0]


def refresh_metadata(conn):
    """Refresh public metadata only, preserving exact source text and hashes."""
    conn.execute(resource('public_metadata.sql'))
    special = conn.execute("SELECT d.source_id,d.part,d.lang,l.kind FROM source_documents d JOIN legislation l ON l.id=d.source_id WHERE d.kind='article' AND d.part~%s",('['+GO_WHITESPACE+']',)).fetchall()
    for sid,part,lang,kind in special:
        url,_ = source_details('article',sid,part,lang,{'kind':kind})
        conn.execute("UPDATE source_documents SET source_url=%s WHERE kind='article' AND source_id=%s AND part=%s AND lang=%s AND source_url IS DISTINCT FROM %s",(url,sid,part,lang,url))


def validate_calculated_current(row, source_text, as_of_date):
    """Validate a canonical export; arithmetic remains owned by the Go exporter."""
    obj = row.get('calculated_current')
    authority = {'law_id':330819, 'article':'1',
                 'url':'https://lebaneselegislation.com/decree/330819/article/1/', 'effective_on':'2024-09-19'}
    if (type(row.get('law_id')) is not int or row['law_id'] != 244565
        or not isinstance(row.get('number'), str) or not row['number']
        or type(row.get('position')) is not int
        or not isinstance(obj, dict)
        or obj.get('kind') != 'statutory_monetary_adjustment'
        or obj.get('is_published_wording') is not False
        or type(obj.get('multiplier')) is not int or obj['multiplier'] != 50
        or obj.get('authority') != authority
        or type(obj['authority'].get('law_id')) is not int
        or as_of_date < date(2024,9,19)
        or not isinstance(source_text,str) or not source_text
        or obj.get('source_text_ar') != source_text
        or row.get('source_sha256') != digest(source_text)
        or obj.get('source_sha256') != row['source_sha256']
        or not isinstance(obj.get('text_ar'),str) or not obj['text_ar'].strip()
        or obj['text_ar'] == source_text):
        raise ValueError('invalid calculated_current source, scope or authority')
    return obj


def validate_calculated_rows(conn, as_of_date):
    rows = conn.execute("""SELECT c.law_id,c.number,c.position,c.source_sha256,c.calculated_current,
        f.text_ar,f.state,f.pending,f.valid_from,l.status,l.issued_on
        FROM article_calculated_current c
        LEFT JOIN article_in_force f ON (f.law_id,f.number,f.position)=(c.law_id,c.number,c.position)
        LEFT JOIN legislation l ON l.id=c.law_id""").fetchall()
    seen = set()
    for law_id,number,position,hashed,obj,text,state,pending,valid_from,status,issued_on in rows:
        key = (law_id,number)
        if (key in seen or state != 'in_force' or pending is not False
            or status is None or status in ('repealed','annulled')
            or valid_from is not None and valid_from > as_of_date
            or issued_on is not None and issued_on > as_of_date):
            raise ValueError('invalid calculated_current article eligibility or duplicate')
        seen.add(key)
        validate_calculated_current({'law_id': law_id,'number': number,'position': position,
            'source_sha256': hashed,'calculated_current': obj},text,as_of_date)


def refresh_calculated_metadata(conn, as_of_date=None):
    # Restored older snapshots can refresh metadata without the optional table.
    if not conn.execute("SELECT to_regclass('public.article_calculated_current')").fetchone()[0]:
        return
    validate_calculated_rows(conn, as_of_date or date.today())
    conn.execute("""UPDATE source_documents d SET metadata=d.metadata || jsonb_build_object('calculated_current',c.calculated_current)
        FROM article_calculated_current c WHERE d.kind='article' AND d.lang='ar'
        AND d.source_id=c.law_id AND d.part=c.number AND d.hash=c.source_sha256
        AND d.text=c.calculated_current->>'source_text_ar'
        AND d.metadata->'calculated_current' IS DISTINCT FROM c.calculated_current""")
