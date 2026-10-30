# syntax=docker/dockerfile:1
ARG POSTGRES_IMAGE=ghcr.io/taktekhq/lebanese-legislation-starter:postgres-18.6-vector0.8.2-r2
FROM ${POSTGRES_IMAGE} AS backend
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 python3-venv ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && python3 -m venv /opt/venv
WORKDIR /opt/starter
COPY pyproject.toml README.md LICENSE ./
COPY src/ ./src/
RUN /opt/venv/bin/pip install --no-cache-dir .
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation-starter"
LABEL org.opencontainers.image.licenses="MIT"
ENV PATH="/opt/venv/bin:$PATH" PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

FROM backend AS db
COPY docker/db-release.py docker/db-health.sh ./docker/
COPY docker/init-reader.sh /docker-entrypoint-initdb.d/20-lebleg-reader.sh
COPY docker/init-release.sh /docker-entrypoint-initdb.d/30-lebleg-release.sh
HEALTHCHECK --interval=10s --timeout=10s --start-period=2h --retries=3 CMD ["bash", "/opt/starter/docker/db-health.sh"]
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["postgres"]

# The API transports share one backend and do not carry a database server.
FROM python:3.13-slim-trixie@sha256:2d44738ebf4ce5f63b9d9ea871e9c7cfdc0426f7b0c97cac915fd88ded1d86bb AS runtime
WORKDIR /opt/starter
COPY pyproject.toml README.md LICENSE ./
COPY src/ ./src/
RUN pip install --no-cache-dir .
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 APP_MODE=api
EXPOSE 8000
USER 65532:65532
ENTRYPOINT ["lebleg"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8000"]

FROM runtime AS mcp
ENV APP_MODE=mcp

# Optional contributor tools retain matching PostgreSQL dump/restore clients.
FROM backend AS publisher
RUN /opt/venv/bin/pip install --no-cache-dir '.[build]'
ENTRYPOINT ["lebleg"]
CMD ["--help"]

# Default source build is the combined API.
FROM runtime AS api
ENV APP_MODE=api
