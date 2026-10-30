"""Indexes for literal substring searches and explicit public citations."""

from importlib.resources import files

SEARCH_INDEX_NAMES = (
    'source_documents_title_trgm',
    'source_documents_text_trgm',
    'source_documents_identifier',
    'source_documents_issued_on',
)


def ensure_search_indexes(conn):
    """Create idempotent search indexes in the caller's build transaction."""
    conn.execute(files('lebleg_starter').joinpath('sql','search.sql').read_text())
    version = conn.execute("SELECT extversion FROM pg_extension WHERE extname='pg_trgm'").fetchone()[0]
    if version != '1.6':
        raise ValueError('search indexes require pg_trgm 1.6')
    return {'extension':'pg_trgm','version':version,'indexes':list(SEARCH_INDEX_NAMES)}
