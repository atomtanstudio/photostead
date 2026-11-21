#!/bin/bash
# Starts the bundled Postgres (socket only, never a network port), then Photostead, and stops
# both cleanly. Photostead runs as PUID:PGID (Unraid's nobody:users, 99:100, by default).
set -euo pipefail

PUID=${PUID:-99}
PGID=${PGID:-100}
PGDATA=/data/postgres
PGBIN=/usr/lib/postgresql/${PG_MAJOR}/bin
export PGHOST=/run/postgresql

mkdir -p /data /run/postgresql
chown postgres:postgres /run/postgresql
chown "$PUID:$PGID" /data

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "photostead: creating the database in $PGDATA"
  install -d -o postgres -g postgres -m 700 "$PGDATA"
  su postgres -s /bin/sh -c "$PGBIN/initdb -D '$PGDATA' --encoding=UTF8 --locale=C.UTF-8 --auth-local=trust --auth-host=reject" >/dev/null
  cat >> "$PGDATA/postgresql.conf" <<CONF
listen_addresses = ''
unix_socket_directories = '/run/postgresql'
max_connections = 60
CONF
fi

# The database's memory, from what this container has (its limit, else the machine's), worked out
# at every start so it follows the machine: an eighth for its cache (128 MB to 2 GB), half as what
# the system caches for it (512 MB to 16 GB), and a thirty-second for building indexes (64 MB to
# 1 GB). The server, the AI worker and ffmpeg need the rest.
mem_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
for limit in /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory/memory.limit_in_bytes; do
  if [ -r "$limit" ] && read -r l < "$limit" && [ "$l" != max ] && [ "$l" -gt 0 ] 2>/dev/null; then
    l=$((l / 1048576))
    if [ "$l" -lt "$mem_mb" ]; then mem_mb=$l; fi
  fi
done
clamp() { local v=$1; [ "$v" -lt "$2" ] && v=$2; [ "$v" -gt "$3" ] && v=$3; echo "$v"; }
shared=$(clamp $((mem_mb / 8)) 128 2048)
cache=$(clamp $((mem_mb / 2)) 512 16384)
maint=$(clamp $((mem_mb / 32)) 64 1024)
echo "photostead: ${mem_mb} MB of memory; the database takes ${shared} MB"
if [ "$mem_mb" -lt 3584 ]; then
  echo "photostead: less than 4 GB of memory: the database, search and faces will be slow (see Health)" >&2
fi
chown -R postgres:postgres "$PGDATA"
# The memory settings go on the command line, which wins over postgresql.conf (where installs made
# before this kept fixed ones).
pgopts="-c shared_buffers=${shared}MB -c effective_cache_size=${cache}MB -c maintenance_work_mem=${maint}MB"
if ! su postgres -s /bin/sh -c "$PGBIN/pg_ctl -D '$PGDATA' -w -t 120 -l '$PGDATA/server.log' -o '$pgopts' start" >/dev/null; then
  echo "photostead: Postgres didn't start; last lines of its log:" >&2
  tail -20 "$PGDATA/server.log" >&2 || true
  exit 1
fi

if ! su postgres -s /bin/sh -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='photostead'\"" | grep -q 1; then
  su postgres -s /bin/sh -c "psql -q -c 'CREATE ROLE photostead LOGIN' && createdb -O photostead photostead"
fi
# Extensions need a superuser, and Photostead's own role isn't one: pgvector (search by meaning).
su postgres -s /bin/sh -c "psql -q -d photostead -c 'CREATE EXTENSION IF NOT EXISTS vector'"

for d in /data/derived /data/tmp; do mkdir -p "$d"; done
chown "$PUID:$PGID" /data/derived /data/tmp

# The photostead share: Photostead files uploads and phone backups in library/ and keeps database
# copies in backups/. Make those two for PUID:PGID if they aren't there yet (only them, never what
# was already in the share), so a share owned by root still works.
SHARE=${PHOTOSTEAD_MANAGED_ROOT:-/photos}
if [ -d "$SHARE" ]; then
  for d in "$SHARE/library" "$SHARE/backups"; do
    if [ ! -e "$d" ] && mkdir "$d" 2>/dev/null; then chown "$PUID:$PGID" "$d"; fi
  done
  if ! setpriv --reuid="$PUID" --regid="$PGID" --clear-groups test -w "$SHARE/library"; then
    echo "photostead: $SHARE/library isn't writable by $PUID:$PGID (PUID, PGID): uploads and phone backups need it" >&2
  fi
fi

stop() {
  local code=${1:-0}
  echo "photostead: stopping"
  if [ -n "${app:-}" ] && kill -0 "$app" 2>/dev/null; then kill -TERM "$app"; wait "$app" || true; fi
  su postgres -s /bin/sh -c "$PGBIN/pg_ctl -D '$PGDATA' -m fast stop" >/dev/null || true
  exit "$code"
}
trap stop TERM INT

setpriv --reuid="$PUID" --regid="$PGID" --clear-groups /usr/local/bin/photostead "${@:-serve}" &
app=$!
code=0
wait "$app" || code=$?
stop "$code"
