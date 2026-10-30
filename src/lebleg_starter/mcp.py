"""Stateless JSON-RPC MCP transport over the shared retrieval and resource services."""

import json
import os
from urllib.parse import urlsplit

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, Response
from starlette.concurrency import run_in_threadpool

from . import __version__
from .general import NOT_ADVICE, GeneralNotFound

PROTOCOL = "2025-06-18"
SUPPORTED = {PROTOCOL, "2025-03-26"}
MAX_BODY = 65536


def _tool(name, description, properties, required):
    return {
        "name": name,
        "description": description,
        "inputSchema": {
            "type": "object",
            "properties": properties,
            "required": required,
            "additionalProperties": False,
        },
    }


TOOLS = [
    _tool(
        "search_legislation",
        "Find original legal passages by keyword, citation or optional semantic retrieval. Machine readings may contain errors. Not legal advice.",
        {
            "query": {"type": "string", "minLength": 1, "maxLength": 500},
            "kind": {"type": "string", "enum": ["laws", "articles", "rulings", "gazette"]},
            "year": {"type": "integer", "minimum": 1, "maximum": 2100},
            "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
            "semantic": {"type": "boolean", "default": True},
            "select": {"type": "boolean", "default": False},
        },
        ["query"],
    ),
    _tool(
        "get_law",
        "Get full law metadata, article outline and recorded amendments. Not legal advice.",
        {"id": {"type": "integer", "minimum": 1}},
        ["id"],
    ),
    _tool(
        "get_article",
        "Get original article text, versions, source links and recorded uncertainty. Not legal advice.",
        {
            "law_id": {"type": "integer", "minimum": 1},
            "number": {"type": "string", "minLength": 1, "maxLength": 100},
        },
        ["law_id", "number"],
    ),
    _tool(
        "get_ruling",
        "Get original ruling text and machine-read pages with source links. The scan remains the reference. Not legal advice.",
        {"id": {"type": "integer", "minimum": 1}},
        ["id"],
    ),
    _tool(
        "get_gazette_issue",
        "Get Gazette metadata and its recorded published texts. Not legal advice.",
        {"id": {"type": "integer", "minimum": 1}},
        ["id"],
    ),
]
SCHEMAS = {tool["name"]: tool["inputSchema"] for tool in TOOLS}


def _error(request_id, code, message):
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}}


def _valid_arguments(name, arguments):
    schema = SCHEMAS[name]
    if not isinstance(arguments, dict) or set(arguments) - schema["properties"].keys():
        return False
    if not set(schema["required"]) <= arguments.keys():
        return False
    for key, value in arguments.items():
        spec = schema["properties"][key]
        if spec["type"] == "integer":
            if type(value) is not int or not spec.get("minimum", value) <= value <= spec.get("maximum", value):
                return False
        elif spec["type"] == "boolean":
            if type(value) is not bool:
                return False
        elif not isinstance(value, str) or not spec.get("minLength", 0) <= len(value) <= spec.get("maxLength", 1000):
            return False
        if "enum" in spec and value not in spec["enum"]:
            return False
    return True


def dispatch(message, retrieval, general):
    request_id = message.get("id")
    method, params = message["method"], message.get("params", {})
    if not isinstance(params, dict):
        return _error(request_id, -32602, "params must be an object")
    if method == "initialize":
        version = params.get("protocolVersion")
        result = {
            "protocolVersion": version if isinstance(version, str) and version in SUPPORTED else PROTOCOL,
            "serverInfo": {"name": "lebaneselegislation", "version": __version__},
            "capabilities": {"tools": {}},
            "instructions": NOT_ADVICE,
        }
    elif method == "ping":
        result = {}
    elif method == "tools/list":
        result = {"tools": TOOLS}
    elif method == "tools/call":
        name, arguments = params.get("name"), params.get("arguments", {})
        if not isinstance(name, str) or name not in SCHEMAS or not _valid_arguments(name, arguments):
            return _error(request_id, -32602, "unknown tool or invalid arguments")
        try:
            if name == "search_legislation":
                result = retrieval.legacy(
                    arguments["query"],
                    kind=arguments.get("kind"),
                    year=arguments.get("year"),
                    limit=arguments.get("limit", 10),
                    semantic=arguments.get("semantic", True),
                    select=arguments.get("select", False),
                )
            elif name == "get_law":
                result = general.law(arguments["id"])
            elif name == "get_article":
                result = general.article(arguments["law_id"], arguments["number"])
            elif name == "get_ruling":
                result = general.ruling(arguments["id"])
            else:
                result = general.gazette(arguments["id"])
            result = {
                "content": [
                    {
                        "type": "text",
                        "text": json.dumps({"result": result, "not_legal_advice": NOT_ADVICE}, ensure_ascii=False),
                    }
                ],
                "isError": False,
            }
        except GeneralNotFound:
            result = {"content": [{"type": "text", "text": "Document not found"}], "isError": True}
        except Exception:  # noqa: BLE001 - credentials and provider errors never cross the transport boundary.
            return _error(request_id, -32603, "source retrieval unavailable")
    else:
        return _error(request_id, -32601, "unknown method")
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def _origin_allowed(request):
    origin = request.headers.get("origin")
    if origin is None:
        return True
    allowed = {value.strip().rstrip("/") for value in os.getenv("MCP_ALLOWED_ORIGINS", "").split(",") if value.strip()}
    if origin.rstrip("/") in allowed:
        return True
    try:
        parsed = urlsplit(origin)
        return (
            parsed.hostname in {"localhost", "127.0.0.1", "::1"}
            and parsed.scheme == request.url.scheme
            and parsed.netloc == request.headers.get("host")
            and not parsed.username
            and not parsed.password
            and not parsed.path
            and not parsed.query
            and not parsed.fragment
        )
    except ValueError:
        return False


def create_router(retrieval_factory, general_factory):
    router = APIRouter()

    @router.api_route("/mcp", methods=["GET", "POST", "OPTIONS"])
    @router.api_route("/mcp/", methods=["GET", "POST", "OPTIONS"], include_in_schema=False)
    async def mcp(request: Request):
        if not _origin_allowed(request):
            return JSONResponse(_error(None, -32600, "origin not allowed"), status_code=403)
        version = request.headers.get("mcp-protocol-version")
        if version and version not in SUPPORTED:
            return JSONResponse(_error(None, -32600, "unsupported protocol version"), status_code=400)
        headers = {"MCP-Protocol-Version": version or PROTOCOL, "Allow": "POST, OPTIONS"}
        if request.headers.get("origin"):
            headers.update(
                {
                    "Access-Control-Allow-Origin": request.headers["origin"],
                    "Vary": "Origin",
                    "Access-Control-Allow-Methods": "POST, GET, OPTIONS",
                    "Access-Control-Allow-Headers": "Content-Type, MCP-Protocol-Version, Authorization",
                    "Access-Control-Expose-Headers": "MCP-Protocol-Version",
                }
            )
        if request.method == "GET":
            return Response("Use POST for stateless MCP requests", status_code=405, headers=headers)
        if request.method == "OPTIONS":
            return Response(status_code=204, headers=headers)
        if request.headers.get("content-type", "").split(";")[0].strip().lower() != "application/json":
            return JSONResponse(_error(None, -32600, "application/json is required"), status_code=415, headers=headers)
        accept = request.headers.get("accept", "*/*")
        if "*/*" not in accept and "application/json" not in accept:
            return Response(status_code=406, headers=headers)
        body = bytearray()
        async for chunk in request.stream():
            body.extend(chunk)
            if len(body) > MAX_BODY:
                return JSONResponse(_error(None, -32600, "request body too large"), status_code=413, headers=headers)
        try:

            def invalid_constant(value):
                raise ValueError("invalid numeric constant")

            message = json.loads(body, parse_constant=invalid_constant)
        except (ValueError, UnicodeDecodeError):
            return JSONResponse(_error(None, -32700, "invalid JSON"), status_code=400, headers=headers)
        if (
            not isinstance(message, dict)
            or message.get("jsonrpc") != "2.0"
            or not isinstance(message.get("method"), str)
            or not message["method"]
            or ("id" in message and (type(message["id"]) not in (int, str) and message["id"] is not None))
        ):
            return JSONResponse(_error(None, -32600, "invalid JSON-RPC request"), status_code=400, headers=headers)
        if "id" not in message:
            return Response(status_code=202, headers=headers)
        result = await run_in_threadpool(dispatch, message, retrieval_factory(), general_factory())
        return JSONResponse(result, headers=headers)

    return router
