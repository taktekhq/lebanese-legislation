# syntax=docker/dockerfile:1
ARG POSTGRES_IMAGE=ghcr.io/taktekhq/lebanese-legislation-starter:postgres-18.6-vector0.8.2
FROM ${POSTGRES_IMAGE} AS runtime
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
ENTRYPOINT ["lebleg"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8000"]

FROM runtime AS publisher
RUN /opt/venv/bin/pip install --no-cache-dir '.[build]'
