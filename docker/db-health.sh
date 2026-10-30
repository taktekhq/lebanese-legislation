#!/bin/bash
set -euo pipefail
# Socket-only init PostgreSQL must never satisfy readiness.
pg_isready -h 127.0.0.1 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" >/dev/null
exec /opt/venv/bin/python /opt/starter/docker/db-release.py health
