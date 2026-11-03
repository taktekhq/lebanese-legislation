#!/bin/bash
set -euo pipefail
pg_isready -h 127.0.0.1 -U postgres -d lebleg -q
psql -h 127.0.0.1 -U postgres -d lebleg -XtAc "SELECT 'legislation'::regclass, 'article_in_force'::regclass" >/dev/null
psql -h 127.0.0.1 -U postgres -d lebleg_search -XtAc "SELECT 'semantic_documents'::regclass, 'semantic_chunks'::regclass" >/dev/null
