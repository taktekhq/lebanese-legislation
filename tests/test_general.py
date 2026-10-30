from datetime import date
from types import SimpleNamespace

import psycopg
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lebleg_starter.general import GeneralError, GeneralNotFound, GeneralService, create_router, date_bound, list_params


@pytest.mark.parametrize(
    ("value", "end", "expected"),
    [
        ("2020", False, date(2020, 1, 1)),
        ("2020", True, date(2020, 12, 31)),
        ("2020-02", True, date(2020, 2, 29)),
        ("2020-02-03", True, date(2020, 2, 3)),
    ],
)
def test_inclusive_date_bounds(value, end, expected):
    assert date_bound(value, end) == expected


@pytest.mark.parametrize("value", ["2020-2", "20", "2021-02-29", "2020-13", "2020-01-01;SELECT 1"])
def test_invalid_date_bounds(value):
    with pytest.raises(ValueError, match="not a date"):
        date_bound(value)


def test_list_parameters_match_go_defaults_and_clamping():
    p = list_params(limit="bad", offset="bad", since=" 2020 ", order="")
    assert (p["limit"], p["offset"], p["since"], p["order"]) == (50, 0, "2020", "newest")
    assert list_params(limit="999", offset="-1")["limit"] == 100
    assert list_params(limit="0", offset="-1")["offset"] == 0
    with pytest.raises(ValueError, match="order"):
        list_params(order="random")


def test_router_is_lazy_and_sanitizes_database_errors():
    app = FastAPI()
    app.include_router(create_router(lambda: app.state.general))

    class Service:
        def root(self):
            return {"version": "v1"}

        def law(self, ident):
            if ident == 1:
                raise GeneralNotFound("law not found")
            if ident == 2:
                raise psycopg.OperationalError("postgresql://owner:SECRET@host/db")
            raise GeneralError("service unavailable")

        def laws(self, **query):
            return list_params(**query)

    app.state.general = Service()
    with TestClient(app) as client:
        assert client.get("/api/v1/").json() == {"version": "v1"}
        assert client.get("/api/v1/law/1/").json() == {"error": "law not found"}
        assert client.get("/api/v1/law/invalid/").status_code == 404
        for ident in (2, 3):
            response = client.get(f"/api/v1/law/{ident}/")
            assert response.status_code == 503
            assert response.json() == {"error": "service unavailable"}
            assert response.headers["cache-control"] == "no-store"
        assert client.get("/api/v1/laws/?since=bad").status_code == 400


def test_pool_reused_and_public_url_validated():
    search = SimpleNamespace(pool=object())
    assert GeneralService(search).pool is search.pool
    for value in ["file:///tmp/file", "https://user:password@example.com"]:
        with pytest.raises(ValueError):
            GeneralService(search, value)
