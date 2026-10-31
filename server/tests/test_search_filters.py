from contextlib import contextmanager

import pytest
from test_search_runtime import Result, manifest, source_row

from lebleg.search import SearchError, SearchService


def test_search_filters_apply_before_limit_and_provider_candidates():
    statements = []

    class Connection:
        def execute(self, query, params=None):
            statements.append((query, params))
            if "SELECT r.manifest" in query:
                return Result([{"manifest": manifest()}])
            if "count(" in query:
                return Result([{"kind": "article", "count": 1}])
            if "FROM source_documents WHERE" in query:
                return Result(
                    [source_row(1, "original source")] if "title ILIKE" in query and "AND NOT" not in query else []
                )
            raise AssertionError(query)

    @contextmanager
    def connection():
        yield Connection()

    service = SearchService.__new__(SearchService)
    service.provider = None
    service.connection = connection
    result = service.search("original", kinds=["article"], year=2020, include_counts=True)
    assert result["exact_counts"] == {"article": 1}
    for query, params in statements[1:]:
        assert "kind = ANY(%s)" in query
        assert ["article"] in params
        assert "2020" in params
    with pytest.raises(SearchError):
        service.search("original", kinds=["injected"])
