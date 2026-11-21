#!/usr/bin/env bash
# Builds the Photostead image on the Unraid server itself (native x86-64, nothing heavy over the
# network). Sends only the source the build needs: never .env files, never library data.
set -euo pipefail
[ -f "$(dirname "$0")/.env" ] && . "$(dirname "$0")/.env"  # UNRAID_HOST=root@tower.local, kept out of git
HOST=${UNRAID_HOST:?set UNRAID_HOST, e.g. root@tower.local, or put it in deploy/unraid/.env}
TAG=${TAG:-photostead:dev}
cd "$(dirname "$0")/../.."
ssh "$HOST" 'mkdir -p /tmp/photostead-build/deploy'
rsync -az --delete --exclude node_modules --exclude dist --exclude '*.tsbuildinfo' --exclude 'internal/testdata' \
  --exclude '__pycache__' --exclude '*.egg-info' --exclude '.venv' \
  server web worker .dockerignore "$HOST:/tmp/photostead-build/"
rsync -az --delete deploy/docker "$HOST:/tmp/photostead-build/deploy/"
ssh "$HOST" "cd /tmp/photostead-build && docker build -f deploy/docker/Dockerfile -t $TAG ."
