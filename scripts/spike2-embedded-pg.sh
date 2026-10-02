#!/usr/bin/env bash
# Spike 2: can the release-form container (non-root, no Docker socket) run an
# embedded throwaway PostgreSQL for automatic restore verification?
# See docs/adr/ADR-004 for the recorded conclusion.
set -euo pipefail
IMAGE="${IMAGE:-postgres:18-alpine}"
echo "Running embedded-PG spike in $IMAGE as the image's non-root postgres user (UID 70)..."
docker run --rm --user 70:70 "$IMAGE" sh -c '
  set -eu
  echo "whoami: $(id -u):$(id -g)"
  mkdir -p /tmp/pgdata /tmp/pgsock
  T0=$(date +%s)
  initdb -D /tmp/pgdata -A trust -U verifier >/dev/null
  echo "initdb: OK"
  pg_ctl -D /tmp/pgdata -l /tmp/pg.log -o "-c listen_addresses= -c unix_socket_directories=/tmp/pgsock -c fsync=off -c synchronous_commit=off" -w start >/dev/null
  echo "server start: OK (unix socket only, no TCP)"
  psql -h /tmp/pgsock -U verifier -d postgres -qc "CREATE TABLE spike(id int primary key, t text); INSERT INTO spike SELECT g, md5(g::text) FROM generate_series(1,100000) g;"
  pg_dump -h /tmp/pgsock -U verifier -Fc -f /tmp/dump.dump postgres
  echo "pg_dump -Fc: OK ($(du -h /tmp/dump.dump | cut -f1))"
  psql -h /tmp/pgsock -U verifier -d postgres -qc "DROP TABLE spike;"
  pg_restore -h /tmp/pgsock -U verifier -d postgres --exit-on-error /tmp/dump.dump
  N=$(psql -h /tmp/pgsock -U verifier -d postgres -tAc "SELECT COUNT(*) FROM spike;")
  echo "pg_restore --exit-on-error: OK (rows=$N)"
  T1=$(date +%s)
  pg_ctl -D /tmp/pgdata -m fast -w stop >/dev/null
  echo "stop+cleanup: OK"
  echo "wall time: $((T1-T0))s; data dir: $(du -sh /tmp/pgdata | cut -f1)"
  [ "$N" = "100000" ] && echo "SPIKE_OK"
'
