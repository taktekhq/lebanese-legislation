# syntax=docker/dockerfile:1
# One Python backend serves both the REST API and the MCP server; APP_MODE picks which.
# Build from the repository root:
#   docker build -f server/Dockerfile --target api -t lebanese-legislation-api .
#   docker build -f server/Dockerfile --target mcp -t lebanese-legislation-mcp .
# The services carry no database server; use the db image or your own PostgreSQL.
FROM python:3.13-slim-trixie@sha256:2d44738ebf4ce5f63b9d9ea871e9c7cfdc0426f7b0c97cac915fd88ded1d86bb AS runtime
WORKDIR /opt/lebleg
COPY LICENSE ./
COPY server/pyproject.toml ./server/
COPY server/src/ ./server/src/
RUN pip install --no-cache-dir ./server
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.licenses="MIT"
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 APP_MODE=api
EXPOSE 8000
USER 65532:65532
ENTRYPOINT ["lebleg"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8000"]

# Published as ghcr.io/taktekhq/lebanese-legislation-mcp. Port 8080 is its published default.
FROM runtime AS mcp
ENV APP_MODE=mcp
EXPOSE 8080
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]

# Default target, published as ghcr.io/taktekhq/lebanese-legislation-api.
FROM runtime AS api
ENV APP_MODE=api
