#!/bin/bash
set -euo pipefail
# Runs after init-reader.sh, against the official image's socket-only init server.
/opt/venv/bin/python /opt/lebleg/db/release.py init
bash /opt/lebleg/db/web-schema.sh
