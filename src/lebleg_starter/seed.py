"""Seed compatible search vectors using current canonical text, without model calls."""

import argparse
import json
import math
import os
import sys
from pathlib import Path

import psycopg
from psycopg.types.json import Jsonb

from .chunks import DIMENSIONS, MODEL, chunks

DEFAULT_QUERY = "SELECT kind,source_id,part,lang,title,text FROM public.source_documents"
_COLUMNS = ("kind", "source_id", "part", "lang", "title", "text")
_EMBEDDING = {
    "model": MODEL,
    "dimensions": DIMENSIONS,
    "task_type": "RETRIEVAL_DOCUMENT",
    "chunk_size": 900,
    "chunk_overlap": 120,
}


class SeedError(ValueError):
    """An operator-safe error which never includes a connection string."""


def _check_schema(conn, label):
    row = conn.execute("""SELECT format_type(atttypid,atttypmod) FROM pg_attribute
        WHERE attrelid='public.semantic_chunks'::regclass AND attname='embedding' AND NOT attisdropped""").fetchone()
    if not row or row[0] != "halfvec(768)":
        raise SeedError(label + " requires compatible halfvec(768) storage")
    key = conn.execute("""SELECT array_agg(a.attname ORDER BY k.ordinality) FROM pg_constraint c
        CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY k(attnum,ordinality)
        JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum
        WHERE c.conrelid='public.semantic_chunks'::regclass AND c.contype='p'""").fetchone()
    if not key or key[0] != ["model", "kind", "source_id", "part", "lang", "hash", "chunk_start"]:
        raise SeedError(label + " requires a compatible semantic_chunks primary key")


def _check_source(source):
    releases = source.execute("SELECT manifest FROM public.lebleg_release").fetchall()
    try:
        if len(releases) != 1:
            raise ValueError()
        spec = releases[0][0]["embedding"]
        if {k: v for k, v in spec.items() if k not in ("storage", "provider")} != _EMBEDDING:
            raise ValueError()
        if spec.get("storage", "halfvec(768)") != "halfvec(768)" or spec.get("provider", "gemini") != "gemini":
            raise ValueError()
    except (KeyError, ValueError, TypeError, AttributeError):
        raise SeedError("source embedding contract is incompatible") from None
    version = source.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone()
    if not version or version[0] != "0.8.2":
        raise SeedError("source requires pgvector 0.8.2")
    _check_schema(source, "source")


def _references(items):
    return [
        {
            "kind": ref[0],
            "source_id": ref[1],
            "part": ref[2],
            "lang": ref[3],
            "hash": chunk.hash,
            "chunk_start": chunk.start,
        }
        for ref, chunk in items
    ]


def _key(ref, chunk):
    return (*ref[:4], chunk.hash, chunk.start)


def _existing(target, items):
    rows = target.execute(
        """SELECT c.kind,c.source_id,c.part,c.lang,c.hash,c.chunk_start,c.chunk_end,c.text
        FROM public.semantic_chunks c JOIN jsonb_to_recordset(%s::jsonb)
        AS r(kind text,source_id integer,part text,lang text,hash text,chunk_start integer)
        USING(kind,source_id,part,lang,hash,chunk_start) WHERE c.model=%s""",
        (Jsonb(_references(items)), MODEL),
    ).fetchall()
    return {tuple(row[:6]): (row[6], row[7]) for row in rows}


def _read_candidates(source, pending):
    rows = source.execute(
        """SELECT c.kind,c.source_id,c.part,c.lang,c.hash,c.chunk_start,c.chunk_end,c.text,c.embedding::text
        FROM public.semantic_chunks c JOIN jsonb_to_recordset(%s::jsonb)
        AS r(kind text,source_id integer,part text,lang text,hash text,chunk_start integer)
        USING(kind,source_id,part,lang,hash,chunk_start) WHERE c.model=%s""",
        (Jsonb(_references(pending)), MODEL),
    ).fetchall()
    return {tuple(row[:6]): (row[6], row[7], row[8]) for row in rows}


def _valid_vector(encoded):
    try:
        vector = json.loads(encoded)
        return (
            isinstance(vector, list)
            and len(vector) == DIMENSIONS
            and all(type(value) in (int, float) and math.isfinite(value) for value in vector)
            and any(value != 0 for value in vector)
        )
    except (TypeError, ValueError, OverflowError):
        return False


def _write_group(target, rows):
    if not rows:
        return 0
    with target.cursor() as writer:
        writer.executemany(
            """INSERT INTO public.semantic_chunks AS existing
            (model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
            VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::halfvec)
            ON CONFLICT(model,kind,source_id,part,lang,hash,chunk_start) DO UPDATE
            SET title=excluded.title,text=excluded.text,chunk_end=excluded.chunk_end,
                embedding=excluded.embedding,updated_at=now()
            WHERE existing.text IS DISTINCT FROM excluded.text OR existing.chunk_end IS DISTINCT FROM excluded.chunk_end""",
            rows,
        )
        return writer.rowcount


def _canonical_items(rows):
    items = []
    for row in rows:
        if (
            len(row) != 6
            or any(not isinstance(row[i], str) for i in (0, 2, 3, 4, 5))
            or type(row[1]) is not int
            or not -(2**31) <= row[1] < 2**31
            or not row[0]
            or not row[3]
        ):
            raise SeedError("canonical query must return six compatible non-null source columns")
        ref = tuple(row[:5])
        items.extend((ref, chunk) for chunk in chunks(row[5]))
    return items


def seed(
    source_dsn, canonical_dsn, target_dsn, *, canonical_query=None, group_size=100, limit=0, offset=0, progress=None
):
    """Copy matching source vectors in canonical document groups.

    The supplied canonical SELECT is trusted operator configuration. Both input
    connections enforce read-only transactions. The target commits each group,
    never imports document generation markers and never deletes any rows.
    """
    if type(group_size) is not int or not 1 <= group_size <= 1000:
        raise SeedError("group size must be 1 to 1000")
    if type(limit) is not int or type(offset) is not int or limit < 0 or offset < 0:
        raise SeedError("limit and offset must be non-negative document bounds")
    query = canonical_query or DEFAULT_QUERY
    if not isinstance(query, str) or not query.strip():
        raise SeedError("canonical query is required")
    query = query.strip().removesuffix(";").rstrip()
    stats = {key: 0 for key in ("documents", "chunks", "skipped", "seeded", "unavailable", "mismatched", "groups")}
    try:
        with (
            psycopg.connect(source_dsn, connect_timeout=15) as source,
            psycopg.connect(canonical_dsn, connect_timeout=15) as canonical,
            psycopg.connect(target_dsn, connect_timeout=15, autocommit=True) as target,
        ):
            source.read_only = True
            canonical.read_only = True
            _check_source(source)
            _check_schema(target, "target")
            target.execute("SELECT pg_advisory_lock(hashtextextended('lebleg-semantic:' || %s,0))", (MODEL,))
            with canonical.cursor(name="lebleg_seed_canonical") as reader:
                reader.execute(
                    "SELECT kind,source_id,part,lang,title,text FROM ("
                    + query
                    + ") canonical ORDER BY kind,source_id,part,lang OFFSET %s LIMIT %s",
                    (offset, limit or None),
                )
                if tuple(column.name for column in reader.description) != _COLUMNS:
                    raise SeedError("canonical query must return the six source columns")
                while batch := reader.fetchmany(group_size):
                    items = _canonical_items(batch)
                    counts = {key: 0 for key in stats}
                    counts["documents"] = len(batch)
                    counts["chunks"] = len(items)
                    counts["groups"] = 1
                    with target.transaction():
                        existing = _existing(target, items) if items else {}
                        pending = []
                        for ref, chunk in items:
                            if existing.get(_key(ref, chunk)) == (chunk.end, chunk.text):
                                counts["skipped"] += 1
                            else:
                                pending.append((ref, chunk))
                        candidates = _read_candidates(source, pending) if pending else {}
                        accepted = []
                        for ref, chunk in pending:
                            candidate = candidates.get(_key(ref, chunk))
                            if candidate is None:
                                counts["unavailable"] += 1
                            elif (
                                candidate[0] != chunk.end
                                or candidate[1] != chunk.text
                                or not _valid_vector(candidate[2])
                            ):
                                counts["mismatched"] += 1
                            else:
                                accepted.append(
                                    (MODEL, *ref, chunk.text, chunk.hash, chunk.start, chunk.end, candidate[2])
                                )
                        counts["seeded"] = _write_group(target, accepted)
                    for key, value in counts.items():
                        stats[key] += value
                    if progress is not None:
                        progress(dict(stats))
        return stats
    except SeedError:
        raise
    except Exception:  # noqa: BLE001 - connection/provider details must never expose operator DSNs.
        raise SeedError(
            "seed failed; completed groups remain saved, check connections and compatible schemas"
        ) from None


class _Parser(argparse.ArgumentParser):
    def error(self, message):
        self.print_usage(sys.stderr)
        self.exit(2, "seed: invalid options or missing connection configuration\n")


def main(argv=None):
    parser = _Parser(description=__doc__)
    for name in ("source", "canonical", "target"):
        parser.add_argument("--" + name + "-dsn", default=os.getenv("SEED_" + name.upper() + "_DATABASE_URL"))
    parser.add_argument(
        "--canonical-query-file", help="Trusted operator SQL SELECT returning kind,source_id,part,lang,title,text"
    )
    parser.add_argument("--group-size", type=int, default=100)
    parser.add_argument("--limit", type=int, default=0, help="Maximum canonical documents; zero means all")
    parser.add_argument("--offset", type=int, default=0, help="Canonical document offset in stable reference order")
    args = parser.parse_args(argv)
    if not all((args.source_dsn, args.canonical_dsn, args.target_dsn)):
        parser.error("three database connections are required")
    try:
        query = Path(args.canonical_query_file).read_text() if args.canonical_query_file else None
        result = seed(
            args.source_dsn,
            args.canonical_dsn,
            args.target_dsn,
            canonical_query=query,
            group_size=args.group_size,
            limit=args.limit,
            offset=args.offset,
        )
        print(json.dumps(result))
        return 0
    except (SeedError, OSError, UnicodeError):
        print(
            "seed failed; completed groups remain saved, check connections, query and release compatibility",
            file=sys.stderr,
        )
        return 1
