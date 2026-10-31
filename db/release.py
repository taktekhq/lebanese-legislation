"""Fresh-volume restore and offline readiness for the database image."""

import os
import subprocess
import sys
from pathlib import Path

import psycopg
from psycopg.conninfo import make_conninfo

from lebleg.manifest import download_release, load_manifest, validate_manifest
from lebleg.restore import manifest_checksum, restore


def database_url():
    # The official image's temporary init server only listens on this Unix socket.
    user = os.getenv("POSTGRES_USER", "postgres")
    password = os.getenv("POSTGRES_PASSWORD")
    if password is None and os.getenv("POSTGRES_PASSWORD_FILE"):
        password = Path(os.environ["POSTGRES_PASSWORD_FILE"]).read_text().rstrip("\n")
    return make_conninfo(
        host="/var/run/postgresql",
        dbname=os.getenv("POSTGRES_DB") or user,
        user=user,
        password=password,
        connect_timeout=5,
    )


def initialize():
    local = os.getenv("LEBLEG_MANIFEST_FILE")
    if local:
        manifest = load_manifest(local)
        dump = os.getenv("LEBLEG_DUMP_FILE") or str(Path(local).parent / manifest["dump"]["path"])
    else:
        manifest, dump = download_release(
            os.getenv("DATASET_REPO", "taktekhq/lebanese-legislation"),
            os.getenv("DATASET_REVISION", "main"),
            os.getenv("MANIFEST_PATH", "releases/latest.json"),
            os.getenv("LEBLEG_CACHE_DIR", str(Path(os.environ["PGDATA"]) / "release-cache")),
        )
    reader = "lebleg_reader" if os.getenv("LEBLEG_READER_PASSWORD") else None
    print(restore(manifest, dump, database_url(), reader), flush=True)


def healthy():
    # A receipt can exist before the socket-only initialization server stops.
    # Require the final TCP listener before inspecting that receipt locally.
    subprocess.run(
        [
            "pg_isready",
            "-h",
            "127.0.0.1",
            "-p",
            os.getenv("PGPORT", "5432"),
            "-U",
            os.getenv("POSTGRES_USER", "postgres"),
            "-d",
            os.getenv("POSTGRES_DB", "postgres"),
        ],
        check=True,
        timeout=5,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    with psycopg.connect(database_url(), autocommit=True) as conn:
        row = conn.execute(
            """SELECT t.manifest,r.manifest,r.release_id,current_setting('server_version_num')::int,
            (SELECT extversion FROM pg_extension WHERE extname='vector')
            FROM public.restore_receipt t CROSS JOIN public.lebleg_release r WHERE t.singleton"""
        ).fetchall()
    if len(row) != 1:
        raise ValueError("one verified release receipt is required")
    manifest, internal, release_id, version, vector = row[0]
    validate_manifest(manifest)
    if version != 180006 or vector != "0.8.2" or release_id != manifest["release_id"]:
        raise ValueError("release runtime mismatch")
    for key in ("source", "embedding", "counts", "text_search"):
        if manifest.get(key) != internal.get(key):
            raise ValueError("release receipt mismatch")
    # An explicitly supplied local release must still match on subsequent boots.
    if os.getenv("LEBLEG_MANIFEST_FILE"):
        expected = load_manifest(os.environ["LEBLEG_MANIFEST_FILE"])
        if manifest_checksum(expected) != manifest_checksum(manifest):
            raise ValueError("selected local manifest does not match restored volume")
    return True


if __name__ == "__main__":
    try:
        if sys.argv[1:] == ["health"]:
            healthy()
        elif sys.argv[1:] == ["init"]:
            initialize()
        else:
            raise ValueError("expected init or health")
    except (ValueError, OSError, psycopg.Error, subprocess.SubprocessError) as error:
        # Do not log connection details, provider keys or artifact URLs.
        print(
            f"database release {sys.argv[1:2]} failed ({type(error).__name__}); check snapshot availability and volume state",
            file=sys.stderr,
        )
        sys.exit(1)
