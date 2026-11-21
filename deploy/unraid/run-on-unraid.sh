#!/usr/bin/env bash
# (Re)creates the Photostead container on Unraid with the same settings as the Unraid template
# (deploy/unraid/photostead.xml), so it shows up on the Docker tab and can be edited there.
# Existing folders are mounted read-only; app data lives in appdata/photostead. The photostead
# share is mounted twice: /photos (cache, then array: how the app reads it and where new files
# land) and /photos-array (the array-only view: where a large migration writes, bypassing the cache).
set -euo pipefail
[ -f "$(dirname "$0")/.env" ] && . "$(dirname "$0")/.env"  # UNRAID_HOST=root@tower.local, kept out of git
HOST=${UNRAID_HOST:?set UNRAID_HOST, e.g. root@tower.local, or put it in deploy/unraid/.env}
scp -q "$(dirname "$0")/photostead.xml" "$HOST:/boot/config/plugins/dockerMan/templates-user/my-photostead.xml"
ssh "$HOST" 'set -e
mkdir -p /mnt/user/appdata/photostead
docker stop -t 60 photostead >/dev/null 2>&1 || true  # let Postgres shut down cleanly
docker rm photostead >/dev/null 2>&1 || true
docker run -d --name=photostead --net=bridge --restart=unless-stopped --stop-timeout 60 --shm-size=1g \
  -e TZ="$(readlink /etc/localtime | sed "s|.*zoneinfo/||")" -e HOST_OS="Unraid" -e HOST_HOSTNAME="$(hostname)" -e HOST_CONTAINERNAME="photostead" \
  -e PUID=99 -e PGID=100 \
  -l net.unraid.docker.managed=dockerman -l "net.unraid.docker.webui=http://[IP]:[PORT:4800]/" \
  -p 4800:4800/tcp \
  -v /mnt/user/appdata/photostead:/data:rw \
  -v /mnt/user/photostead:/photos:rw \
  -v /mnt/user0/photostead:/photos-array:rw \
  -e PHOTOSTEAD_MANAGED_ROOT=/photos -e PHOTOSTEAD_MOVE_TARGET=/photos-array \
  -v /mnt/user/storage/images:/libraries/images:ro \
  -v /mnt/user/storage/videos:/libraries/videos:ro \
  photostead:dev >/dev/null
for i in $(seq 1 90); do curl -sf -o /dev/null http://127.0.0.1:4800/api/v1/setup && { echo "Photostead is up on port 4800"; exit 0; }; sleep 1; done
echo "Photostead did not start; recent logs:"; docker logs --tail 30 photostead; exit 1'
