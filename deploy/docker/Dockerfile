# Photostead: one container with the server, the web app and Postgres (decision 9.x), for 64-bit
# x86 and ARM (linux/amd64, linux/arm64).
# Build from the repository root:  docker build -f deploy/docker/Dockerfile -t photostead .
# The server links libvips (cgo), so it's built on the platform it's for; the web app is the same
# everywhere, so it's built once, on the machine doing the building.

ARG PG_MAJOR=18

FROM --platform=$BUILDPLATFORM node:24-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-trixie AS server
RUN apt-get update && apt-get install -y --no-install-recommends libvips-dev pkg-config && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/photostead ./cmd/photostead

# The built-in AI worker (search embeddings on the CPU), in its own Python environment.
FROM debian:trixie-slim AS worker
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-venv && rm -rf /var/lib/apt/lists/*
COPY worker/ /src/worker/
RUN python3 -m venv /opt/photostead-worker && /opt/photostead-worker/bin/pip install --no-cache-dir "/src/worker[cpu]"

FROM debian:trixie-slim
ARG PG_MAJOR
ENV PG_MAJOR=${PG_MAJOR} LANG=C.UTF-8
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gnupg tini \
 && install -d /usr/share/postgresql-common/pgdg \
 && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
 && echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt trixie-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
 && apt-get update && apt-get install -y --no-install-recommends \
      postgresql-${PG_MAJOR} postgresql-${PG_MAJOR}-pgvector \
      libvips42t64 libheif-plugin-libde265 libheif-plugin-dav1d \
      ffmpeg libimage-exiftool-perl python3 \
 && apt-get purge -y curl gnupg && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY --from=server /out/photostead /usr/local/bin/photostead
COPY --from=web /web/dist /app/web
COPY --from=worker /opt/photostead-worker /opt/photostead-worker
COPY deploy/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
ENV PHOTOSTEAD_DATA_DIR=/data \
    PHOTOSTEAD_LISTEN=0.0.0.0:4800 \
    PHOTOSTEAD_WEB_DIR=/app/web \
    PHOTOSTEAD_DATABASE_URL="postgres://photostead@/photostead?host=/run/postgresql"
EXPOSE 4800
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/4800"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
