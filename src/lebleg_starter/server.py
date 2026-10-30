"""General HTTP API and MCP backed by the same read-only retrieval service."""

import argparse
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Query
from fastapi.responses import JSONResponse

from .embedding import Gemini
from .general import GeneralService
from .general import create_router as general_router
from .mcp import create_router as mcp_router
from .retrieval import RetrievalAPI
from .search import SearchError, SearchService


def create_app(dsn=None, key=None, service=None, general_service=None, mode=None):
    mode = mode or os.getenv("APP_MODE", "api")
    if mode not in ("api", "mcp"):
        raise ValueError("APP_MODE must be api or mcp")

    @asynccontextmanager
    async def lifespan(app):
        app.state.service = service or SearchService(
            dsn or os.environ["DATABASE_URL"],
            Gemini(key or os.getenv("GEMINI_API_KEY")) if key or os.getenv("GEMINI_API_KEY") else None,
        )
        app.state.general = general_service or (
            GeneralService(app.state.service, os.getenv("PUBLIC_URL", "https://lebaneselegislation.com"))
            if hasattr(app.state.service, "pool")
            else None
        )
        app.state.retrieval = RetrievalAPI(app.state.service, app.state.general)
        try:
            yield
        finally:
            app.state.service.close()

    app = FastAPI(
        title="Lebanese Legislation API",
        lifespan=lifespan,
        docs_url="/docs" if mode == "api" else None,
        redoc_url=None,
        openapi_url="/openapi.json" if mode == "api" else None,
    )

    @app.get("/healthz")
    def health():
        try:
            if not app.state.service.health():
                raise ValueError()
            return {"ready": True}
        except Exception:  # noqa: BLE001 - no database or provider credentials in responses.
            raise HTTPException(503, "release is not ready") from None

    app.include_router(mcp_router(lambda: app.state.retrieval, lambda: app.state.general))
    if mode == "mcp":
        return app

    app.include_router(general_router(lambda: app.state.general))

    @app.get("/search")
    @app.get("/api/search")
    def search(
        q: str = Query(min_length=1, max_length=500),
        limit: int = Query(default=8, ge=1, le=16),
        semantic: bool = True,
        select: bool = False,
    ):
        try:
            return app.state.retrieval.passages(q, limit, semantic, select)
        except SearchError:
            raise HTTPException(422, "invalid source query") from None
        except Exception:  # noqa: BLE001 - optional provider errors must not reveal credentials.
            raise HTTPException(503, "source retrieval unavailable") from None

    @app.get("/api/v1/search/")
    @app.get("/api/v1/search", include_in_schema=False)
    def legacy_search(
        q: str = Query(default="", max_length=500),
        type: str | None = None,
        year: int = Query(default=0, ge=0, le=2100),
        limit: int = Query(default=10, ge=1, le=50),
        offset: int = Query(default=0, ge=0, le=10000),
        semantic: bool = False,
        select: bool = False,
    ):
        try:
            result = app.state.retrieval.legacy(q, type, year or None, limit, offset, semantic, select)
            headers = {"Cache-Control": "no-store" if semantic or select else "public, max-age=60"}
            return JSONResponse(result, headers=headers)
        except SearchError:
            raise HTTPException(422, "invalid source query") from None
        except Exception:  # noqa: BLE001 - do not leak DSNs or provider errors.
            raise HTTPException(503, "source retrieval unavailable") from None

    return app


def main(argv=None):
    import uvicorn

    parser = argparse.ArgumentParser(description="Serve general legal resources, source retrieval and MCP")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args(argv)
    if not os.getenv("DATABASE_URL"):
        parser.error("DATABASE_URL is required")
    uvicorn.run(create_app(), host=args.host, port=args.port, log_level="warning", access_log=False)
    return 0
