"""Small loopback-first source retrieval HTTP API."""

import argparse
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Query

from .embedding import Gemini
from .search import SearchService


def create_app(dsn=None, key=None, service=None):
    @asynccontextmanager
    async def lifespan(app):
        app.state.service = service or SearchService(
            dsn or os.environ["DATABASE_URL"],
            Gemini(key or os.getenv("GEMINI_API_KEY")) if key or os.getenv("GEMINI_API_KEY") else None,
        )
        try:
            yield
        finally:
            app.state.service.close()

    app = FastAPI(title="Lebanese legislation source retrieval", lifespan=lifespan)

    @app.get("/healthz")
    def health():
        try:
            if not app.state.service.health():
                raise ValueError()
            return {"ready": True}
        except Exception:  # noqa: BLE001 - HTTP errors must never expose database or provider credentials.
            raise HTTPException(503, "release is not ready") from None

    @app.get("/search")
    @app.get("/api/search")
    def search(
        q: str = Query(min_length=1, max_length=500),
        limit: int = Query(default=8, ge=1, le=16),
        semantic: bool = True,
        select: bool = False,
    ):
        try:
            return app.state.service.search(q, limit, semantic, select)
        except Exception:  # noqa: BLE001 - HTTP errors must never expose database or provider credentials.
            raise HTTPException(503, "source retrieval unavailable") from None

    return app


def main(argv=None):
    import uvicorn

    parser = argparse.ArgumentParser(description="Serve original source passages")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args(argv)
    if not os.getenv("DATABASE_URL"):
        parser.error("DATABASE_URL is required")
    uvicorn.run(create_app(), host=args.host, port=args.port, log_level="warning", access_log=False)
    return 0
