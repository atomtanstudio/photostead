#!/usr/bin/env bash
# The App Review demo server on Unraid: a second Photostead, photostead-demo, kept apart from the
# household's own (its own image tag, folder and port; no libraries, no Tailscale). Apple's reviewer
# reaches it as https://demo.photostead.com through a Cloudflare Tunnel of its own, and pairs with
# a lasting code. It only needs to be up while a version is in review.
#
#   deploy/unraid/demo-on-unraid.sh build   # the image (photostead:demo) from this checkout
#   deploy/unraid/demo-on-unraid.sh load    # (re)create it taking uploads, to load the demo household
#                                           # (site/demo-library/load.mjs, SERVER=http://<unraid>:4810)
#   deploy/unraid/demo-on-unraid.sh run     # (re)create it as App Review sees it: uploads thrown away
#   deploy/unraid/demo-on-unraid.sh save    # keep its state now as the clean copy
#   deploy/unraid/demo-on-unraid.sh reset   # put the clean copy back (before each submission)
#
# Paired phones are part of the state, so a reset unpairs them: reset before submitting, never
# during a review. The household's own Photostead (photostead, photostead:dev) isn't touched.
set -euo pipefail
[ -f "$(dirname "$0")/.env" ] && . "$(dirname "$0")/.env"  # UNRAID_HOST=root@tower.local, kept out of git
HOST=${UNRAID_HOST:?set UNRAID_HOST, e.g. root@tower.local, or put it in deploy/unraid/.env}
DATA=/mnt/user/appdata/photostead-demo
CLEAN=/mnt/user/appdata/photostead-demo-clean

create() { # $1: "on" throws uploads away
  scp -q "$(dirname "$0")/photostead-demo.xml" "$HOST:/boot/config/plugins/dockerMan/templates-user/my-photostead-demo.xml"
  ssh "$HOST" "set -e
mkdir -p $DATA/photos && chown 99:100 $DATA/photos  # the share uploads go into
# The models and place names the household's server has already downloaded (2.5 GB), not again.
[ -d $DATA/models ] || { [ -d /mnt/user/appdata/photostead/models ] && cp -a /mnt/user/appdata/photostead/models $DATA/models; } || true
[ -d $DATA/geodata ] || { [ -d /mnt/user/appdata/photostead/geodata ] && cp -a /mnt/user/appdata/photostead/geodata $DATA/geodata; } || true
docker stop -t 60 photostead-demo >/dev/null 2>&1 || true
docker rm photostead-demo >/dev/null 2>&1 || true
# 6 GB at most, so it can't crowd the household's own (the database sizes itself from it).
docker run -d --name=photostead-demo --net=bridge --restart=unless-stopped --stop-timeout 60 --shm-size=1g --memory=6g \
  -e TZ=\"\$(readlink /etc/localtime | sed 's|.*zoneinfo/||')\" -e HOST_OS=Unraid -e HOST_HOSTNAME=\"\$(hostname)\" -e HOST_CONTAINERNAME=photostead-demo \
  -e PUID=99 -e PGID=100 \
  -l net.unraid.docker.managed=dockerman -l 'net.unraid.docker.webui=http://[IP]:[PORT:4810]/' \
  -p 4810:4800/tcp \
  -v $DATA:/data:rw \
  -e PHOTOSTEAD_MANAGED_ROOT=/data/photos -e PHOTOSTEAD_BACKUP_DIR=off \
  -e PHOTOSTEAD_PUBLIC_URL=https://demo.photostead.com \
  -e PHOTOSTEAD_DISCARD_UPLOADS=$1 \
  photostead:demo >/dev/null
# Starts with the array, like the household's own.
grep -qx photostead-demo /var/lib/docker/unraid-autostart 2>/dev/null || echo photostead-demo >> /var/lib/docker/unraid-autostart
for i in \$(seq 1 120); do curl -sf -o /dev/null http://127.0.0.1:4810/api/v1/setup && { echo 'photostead-demo is up on port 4810 (uploads thrown away: $1)'; exit 0; }; sleep 1; done
echo 'photostead-demo did not start; recent logs:'; docker logs --tail 30 photostead-demo; exit 1"
}

case "${1:-}" in
  build) TAG=photostead:demo "$(dirname "$0")/build-on-unraid.sh" ;;
  load) create off ;;
  run) create on ;;
  save)
    ssh "$HOST" "set -e
docker stop -t 60 photostead-demo >/dev/null
rsync -a --delete $DATA/ $CLEAN/
docker start photostead-demo >/dev/null
echo 'Saved the clean copy.'" ;;
  reset)
    ssh "$HOST" "set -e
[ -d $CLEAN/postgres ] || { echo 'No clean copy yet: run save first.' >&2; exit 1; }
docker stop -t 60 photostead-demo >/dev/null
rsync -a --delete $CLEAN/ $DATA/
docker start photostead-demo >/dev/null
echo 'Put the clean copy back; phones paired since need pairing again.'" ;;
  *) echo "usage: $0 build|load|run|save|reset" >&2; exit 2 ;;
esac
