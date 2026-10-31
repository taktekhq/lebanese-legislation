"""Verify and restore a public dump into an empty database. Existing data is never dropped."""

import argparse
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path

import psycopg
from psycopg import sql
from psycopg.types.json import Jsonb

from .manifest import ManifestError, download_release, load_manifest, validate_manifest, verify_file


class RestoreError(ValueError):
    pass


def target_state(conn, manifest):
    tables = [
        row[0]
        for row in conn.execute(
            """SELECT tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')
                UNION SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
                  WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'
                  AND c.relkind IN ('v','m','S','f')
                  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e')
                UNION SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
                  WHERE n.nspname NOT IN ('pg_catalog','information_schema')
                  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')
                UNION SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
                  WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_%'
                  AND t.typrelid=0 AND t.typelem=0
                  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass AND d.objid=t.oid AND d.deptype='e')
                UNION SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('public','pg_catalog','information_schema')
                  AND nspname NOT LIKE 'pg_%'"""
        ).fetchall()
    ]
    if not tables:
        return "empty"
    if "restore_receipt" in tables:
        receipt = conn.execute("SELECT manifest FROM public.restore_receipt WHERE singleton=true").fetchone()
        if receipt and {k: v for k, v in receipt[0].items() if k != "artifact_revision"} == {
            k: v for k, v in manifest.items() if k != "artifact_revision"
        }:
            return "ready"
    if "restore_attempt" in tables:
        attempt = conn.execute("""SELECT a.manifest_sha256,a.owner_oid,c.relowner,current_user::regrole::oid
            FROM public.restore_attempt a JOIN pg_class c ON c.oid='public.restore_attempt'::regclass
            WHERE a.singleton""").fetchone()
        if attempt and attempt[0] == manifest_checksum(manifest) and attempt[1] == attempt[2] == attempt[3]:
            if set(tables) == {"restore_attempt"}:
                return "attempt_empty"
            expected = set(manifest["counts"]["tables"]) | {
                "restore_attempt",
                "source_documents",
                "semantic_chunks",
                "semantic_documents",
                "lebleg_release",
            }
            if set(tables) <= expected:
                return "resume"
    raise RestoreError("target database is occupied or belongs to another release; use a new database")


def manifest_checksum(manifest):
    external = {k: v for k, v in manifest.items() if k != "artifact_revision"}
    return hashlib.sha256(
        json.dumps(external, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    ).hexdigest()


def _check_reader(conn, reader_role):
    if not reader_role:
        return
    role = conn.execute(
        "SELECT rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=%s",
        (reader_role,),
    ).fetchone()
    if not role or any(role):
        raise RestoreError("a restricted existing reader role is required")


def _begin_attempt(conn, manifest):
    with conn.transaction():
        conn.execute("""CREATE TABLE restore_attempt(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
            manifest_sha256 text NOT NULL,manifest jsonb NOT NULL,owner_oid oid NOT NULL,
            started_at timestamptz NOT NULL DEFAULT now())""")
        conn.execute(
            "INSERT INTO restore_attempt(singleton,manifest_sha256,manifest,owner_oid) VALUES(true,%s,%s,current_user::regrole::oid)",
            (manifest_checksum(manifest), Jsonb(manifest)),
        )


def _check_search_indexes(conn, spec):
    extension = conn.execute("SELECT extversion FROM pg_extension WHERE extname=%s", (spec["extension"],)).fetchone()
    if not extension or extension[0] != spec["version"]:
        raise RestoreError("restored text search extension mismatch")
    rows = conn.execute("""SELECT c.relname,i.indisvalid,i.indisready,a.amname,pg_get_indexdef(c.oid)
        FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace
        JOIN pg_am a ON a.oid=c.relam WHERE i.indrelid='public.source_documents'::regclass AND n.nspname='public'""").fetchall()
    indexes = {row[0]: row[1:] for row in rows}
    for name in spec["indexes"]:
        row = indexes.get(name)
        if not row or not row[0] or not row[1]:
            raise RestoreError("restored text search index is missing or invalid")
        method, definition = row[2], row[3].lower()
        if name.endswith("_trgm"):
            column = "title" if name == "source_documents_title_trgm" else "text"
            valid = method == "gin" and f"({column} gin_trgm_ops)" in definition
        else:
            keys = ("identifier", "number") if name.endswith("_identifier") else ("issued_on", "date")
            valid = (
                method == "btree" and "coalesce" in definition and all("'" + key + "'" in definition for key in keys)
            )
        if not valid:
            raise RestoreError("restored text search index definition mismatch")


def check_database(conn, manifest, reader_role=None):
    version = conn.execute("SELECT current_setting('server_version_num')::int").fetchone()[0]
    if version != 180006:
        raise RestoreError("PostgreSQL 18.6 is required")
    ext = conn.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone()
    if not ext or ext[0] != "0.8.2":
        raise RestoreError("pgvector 0.8.2 is required")
    for table, count in {
        **manifest["counts"]["tables"],
        "source_documents": manifest["counts"]["source_documents"],
        "semantic_chunks": manifest["counts"]["semantic_chunks"],
    }.items():
        actual = conn.execute(sql.SQL("SELECT count(*) FROM {}").format(sql.Identifier(table))).fetchone()[0]
        if actual != count:
            raise RestoreError("restored table count mismatch")
    identity = conn.execute("SELECT release_id,manifest FROM lebleg_release").fetchall()
    if len(identity) != 1 or identity[0][0] != manifest["release_id"]:
        raise RestoreError("restored release identity mismatch")
    internal = identity[0][1]
    for key in ("source", "embedding", "counts") + (("text_search",) if "text_search" in manifest else ()):
        if internal.get(key) != manifest[key]:
            raise RestoreError("restored release metadata mismatch")
    if "text_search" in manifest:
        _check_search_indexes(conn, manifest["text_search"])
    vector_type = conn.execute(
        "SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='semantic_chunks'::regclass AND attname='embedding'"
    ).fetchone()
    if not vector_type or vector_type[0] != "halfvec(768)":
        raise RestoreError("restored embedding schema mismatch")
    index = conn.execute(
        "SELECT 1 FROM pg_indexes WHERE tablename='semantic_chunks' AND indexdef LIKE '%USING hnsw%' AND indexdef LIKE '%halfvec_cosine_ops%'"
    ).fetchone()
    if not index:
        raise RestoreError("restored vector index is missing")
    stale = conn.execute("""SELECT 1 FROM source_documents
        WHERE hash IS DISTINCT FROM encode(sha256(convert_to(text,'UTF8')),'hex') LIMIT 1""").fetchone()
    if stale:
        raise RestoreError("restored source hash mismatch")
    stale = conn.execute(
        """SELECT 1 FROM semantic_chunks c LEFT JOIN source_documents d USING(kind,source_id,part,lang)
        WHERE d.hash IS NULL OR c.hash IS DISTINCT FROM d.hash OR c.chunk_start<0
          OR c.chunk_end<=c.chunk_start OR c.chunk_end>char_length(d.text) OR c.chunk_end-c.chunk_start>900
          OR c.text IS DISTINCT FROM substring(d.text from c.chunk_start+1 for c.chunk_end-c.chunk_start)
          OR c.model<>%s LIMIT 1""",
        (manifest["embedding"]["model"],),
    ).fetchone()
    if stale:
        raise RestoreError("restored semantic source mismatch")
    if reader_role:
        role = conn.execute(
            "SELECT rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=%s",
            (reader_role,),
        ).fetchone()
        if not role or any(role):
            raise RestoreError("a restricted existing reader role is required")
        for table in manifest["counts"]["tables"] | {"source_documents": 0, "semantic_chunks": 0, "lebleg_release": 0}:
            access = conn.execute(
                "SELECT has_table_privilege(%s,%s,'SELECT'),has_table_privilege(%s,%s,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')",
                (reader_role, table, reader_role, table),
            ).fetchone()
            if not access[0] or access[1]:
                raise RestoreError("reader table permissions are not read-only")


def _finish_restore(conn, manifest, reader_role):
    # Top-level maintenance releases relation locks between tables. Keeping it
    # with uncommitted GRANT catalog updates can deadlock with autovacuum.
    conn.execute("ANALYZE")
    with conn.transaction():
        # Bootstrap owns grants; restore can apply SELECT only to an already restricted reader.
        if reader_role:
            _check_reader(conn, reader_role)
            conn.execute(sql.SQL("GRANT USAGE ON SCHEMA public TO {}").format(sql.Identifier(reader_role)))
            conn.execute(
                sql.SQL("GRANT SELECT ON ALL TABLES IN SCHEMA public TO {}").format(sql.Identifier(reader_role))
            )
        check_database(conn, manifest, reader_role)
        conn.execute(
            "CREATE TABLE restore_receipt(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),manifest jsonb NOT NULL,restored_at timestamptz NOT NULL DEFAULT now())"
        )
        conn.execute("INSERT INTO restore_receipt(singleton,manifest) VALUES(true,%s)", (Jsonb(manifest),))
        if reader_role:
            conn.execute(sql.SQL("GRANT SELECT ON restore_receipt TO {}").format(sql.Identifier(reader_role)))


def restore(manifest, dump_path, dsn, reader_role="lebleg_reader", pg_restore="pg_restore"):
    validate_manifest(manifest)
    verify_file(dump_path, manifest["dump"])
    try:
        version = subprocess.run([pg_restore, "--version"], capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError):
        raise RestoreError("matching pg_restore executable is required") from None
    if not re.search(r"\b18\.6\b", version):
        raise RestoreError("pg_restore 18.6 is required")
    # The advisory lock spans the child restore connection as well as verification.
    try:
        with psycopg.connect(dsn, autocommit=True) as conn:
            if not conn.execute("SELECT pg_try_advisory_lock(172913,1)").fetchone()[0]:
                raise RestoreError("another restore is running")
            server_version = conn.execute("SELECT current_setting('server_version_num')::int").fetchone()[0]
            if server_version != 180006:
                raise RestoreError("PostgreSQL 18.6 is required")
            _check_reader(conn, reader_role)
            state = target_state(conn, manifest)
            if state == "ready":
                with conn.transaction():
                    check_database(conn, manifest, reader_role)
                return "already restored"
            if state == "resume":
                _finish_restore(conn, manifest, reader_role)
                return "restored"
            if state == "empty":
                _begin_attempt(conn, manifest)
            env = os.environ.copy()
            # Keep connection secrets out of the command line and all error output.
            from psycopg.conninfo import conninfo_to_dict

            for key, value in conninfo_to_dict(dsn).items():
                env[
                    "PG"
                    + {
                        "dbname": "DATABASE",
                        "user": "USER",
                        "password": "PASSWORD",
                        "host": "HOST",
                        "port": "PORT",
                        "sslmode": "SSLMODE",
                        "options": "OPTIONS",
                        "connect_timeout": "CONNECT_TIMEOUT",
                    }.get(key, key.upper())
                ] = str(value)
            result = subprocess.run(
                [
                    pg_restore,
                    "--dbname=" + conninfo_to_dict(dsn).get("dbname", "postgres"),
                    "--no-owner",
                    "--no-acl",
                    "--single-transaction",
                    "--exit-on-error",
                    str(Path(dump_path).resolve()),
                ],
                env=env,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                check=False,
            )
            if result.returncode:
                raise RestoreError("transactional restore failed; target was not marked ready")
            _finish_restore(conn, manifest, reader_role)
            return "restored"
    except psycopg.Error:
        raise RestoreError("database restore or validation failed; target was not marked ready") from None


def main(argv=None):
    parser = argparse.ArgumentParser(description="Restore a verified public release into an empty database")
    parser.add_argument("--manifest")
    parser.add_argument("--dump")
    parser.add_argument("--repo", default="taktekhq/lebanese-legislation")
    parser.add_argument("--revision", default="main")
    parser.add_argument("--manifest-path", default="releases/latest.json")
    parser.add_argument("--cache-dir", default=".cache/lebleg")
    parser.add_argument("--database-url", default=os.getenv("DATABASE_URL"))
    parser.add_argument("--reader-role", default=os.getenv("READER_ROLE", "lebleg_reader"))
    parser.add_argument("--pg-restore", default=os.getenv("PG_RESTORE_BIN", "pg_restore"))
    args = parser.parse_args(argv)
    if not args.database_url:
        parser.error("DATABASE_URL is required")
    try:
        if args.manifest:
            m = load_manifest(args.manifest)
            dump = args.dump or str(Path(args.manifest).parent / m["dump"]["path"])
        else:
            m, dump = download_release(args.repo, args.revision, args.manifest_path, args.cache_dir)
        print(restore(m, dump, args.database_url, args.reader_role, args.pg_restore))
        return 0
    except (RestoreError, ManifestError, OSError, ValueError):
        parser.exit(1, "restore failed; verify manifest, target database and reader permissions\n")
