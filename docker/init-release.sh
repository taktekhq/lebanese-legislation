#!/bin/bash
set -euo pipefail
# Runs after init-reader.sh, against the official image's socket-only init server.
/opt/venv/bin/python /opt/starter/docker/db-release.py init
