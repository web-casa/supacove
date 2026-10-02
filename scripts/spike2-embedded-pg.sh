#!/usr/bin/env bash
# Spike 2 / ADR-004: can the RELEASE runtime shape (Debian slim, non-root
# UID 10001, no Docker socket, no privileges) run an embedded throwaway
# PostgreSQL for automatic restore verification?
#
# Usage: scripts/spike2-embedded-pg.sh [image]
#   default image: supabackup:runtime-spike (built with
#   `docker build --target runtime-spike -t supabackup:runtime-spike .`)
#
# The experiment asserts, inside the container:
#   - initdb + pg_ctl + pg_dump -Fc + pg_restore --exit-on-error all succeed as UID 10001
#   - the temporary instance listens on a Unix socket only (no TCP)
#   - a root-owned 0600 canary is NOT readable by the verifier (UID boundary)
#   - app-owned secrets in the same UID WOULD be reachable (documented boundary,
#     never claimed as sandboxing)
set -euo pipefail
IMAGE="${1:-supabackup:runtime-spike}"
PG_BIN="/usr/lib/postgresql/18/bin"
ROWS="${ROWS:-100000}"

echo "Running embedded-PG spike in [$IMAGE] as UID 10001..."
docker run --rm --entrypoint bash "$IMAGE" -c '
  set -eu
  export PATH="'$PG_BIN':$PATH"
  echo "identity: $(id -u):$(id -g)"
  # Trust-boundary canaries: root-owned secret must be unreadable; a
  # same-UID secret WOULD be readable (that is the documented boundary).
  echo "app-secret-canary" > /tmp/app-owned-secret
  chmod 600 /tmp/app-owned-secret

  mkdir -p /tmp/pgdata /tmp/pgsock
  # Any failure must still stop the instance and remove leftovers; the outer
  # assertions never run when we abort early (review round 4, P1-19).
  cleanup() {
    pg_ctl -D /tmp/pgdata -m fast -w stop >/dev/null 2>&1 || true
    rm -rf /tmp/pgdata /tmp/pgsock /tmp/dump.dump /tmp/truncated.dump /tmp/pg.log
  }
  trap cleanup EXIT
  T0=$(date +%s)
  initdb -D /tmp/pgdata -A trust -U verifier >/dev/null 2>&1
  echo "initdb: OK"
  pg_ctl -D /tmp/pgdata -l /tmp/pg.log \
    -o "-c listen_addresses= -c unix_socket_directories=/tmp/pgsock -c fsync=off -c synchronous_commit=off" \
    -w start >/dev/null
  echo "server start: OK (unix socket only, no TCP listener)"

  psql -h /tmp/pgsock -U verifier -d postgres -qc "
    CREATE TABLE spike(id int primary key, t text);
    INSERT INTO spike SELECT g, md5(g::text) FROM generate_series(1,'$ROWS') g;"
  pg_dump -h /tmp/pgsock -U verifier -Fc -f /tmp/dump.dump postgres
  echo "pg_dump -Fc: OK ($(du -h /tmp/dump.dump | cut -f1))"
  psql -h /tmp/pgsock -U verifier -d postgres -qc "DROP TABLE spike;"
  # Peak RSS sampling of the restore process (resource evidence, review
  # round 2 P1-19): poll /proc during the restore.
  # Sample BOTH the client and the temporary postmaster (server side matters
  # for capacity planning; review round 4, P1-19).
  PM=$(pgrep -f "postgres -D /tmp/pgdata" | head -1 || true)
  pg_restore -h /tmp/pgsock -U verifier -d postgres --exit-on-error /tmp/dump.dump &
  RP=$!
  PEAK=0
  PMPEAK=0
  while kill -0 $RP 2>/dev/null; do
    if [ -r "/proc/$RP/status" ]; then
      KB=$(grep VmHWM "/proc/$RP/status" | tr -s " " | cut -d " " -f 2)
      case "$KB" in ""|*[!0-9]*) KB=0 ;; esac
      if [ "$KB" -gt "$PEAK" ]; then PEAK=$KB; fi
    fi
    if [ -n "$PM" ] && [ -r "/proc/$PM/status" ]; then
      KB=$(grep VmHWM "/proc/$PM/status" | tr -s " " | cut -d " " -f 2)
      case "$KB" in ""|*[!0-9]*) KB=0 ;; esac
      if [ "$KB" -gt "$PMPEAK" ]; then PMPEAK=$KB; fi
    fi
    sleep 0.05
  done
  wait $RP
  echo "pg_restore --exit-on-error: OK (client peak RSS ≈ $((PEAK/1024)) MiB, postmaster ≈ $((PMPEAK/1024)) MiB)"
  N=$(psql -h /tmp/pgsock -U verifier -d postgres -tAc "SELECT COUNT(*) FROM spike;")
  echo "pg_restore --exit-on-error: OK (rows=$N)"
  [ "$N" = "'$ROWS'" ] || { echo "ROW MISMATCH"; exit 2; }

  # Corruption detection on a CLEAN target: drop what the restore created so
  # an "object already exists" error cannot masquerade as rejection. The
  # truncation point adapts to the archive size, and the rejection reason is
  # preserved for evidence (review round 2/4, P1-19).
  psql -h /tmp/pgsock -U verifier -d postgres -qc "DROP TABLE IF EXISTS spike;"
  SZ=$(wc -c < /tmp/dump.dump)
  HALF=$((SZ / 2))
  [ "$HALF" -gt 0 ] || HALF=1
  head -c "$HALF" /tmp/dump.dump > /tmp/truncated.dump
  if pg_restore -h /tmp/pgsock -U verifier -d postgres --exit-on-error /tmp/truncated.dump 2>/tmp/truncated-error.log; then
    echo "ERROR: truncated dump restored cleanly"; exit 3
  fi
  echo "truncated archive rejected: OK ($(wc -c < /tmp/truncated.dump) of $SZ bytes; reason: $(head -1 /tmp/truncated-error.log))"

  pg_ctl -D /tmp/pgdata -m fast -w stop >/dev/null
  # Give exiting backends a moment, then assert nothing survives.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "[p]ostgres.*pgdata" >/dev/null 2>&1 || break
    sleep 0.5
  done
  rm -rf /tmp/pgdata /tmp/pgsock /tmp/dump.dump /tmp/truncated.dump /tmp/pg.log
  T1=$(date +%s)
  # Failure-cleanup assertions: no postmaster, no sockets, no data left over.
  if pgrep -af "[p]ostgres" | grep -v pgrep; then
    echo "CLEANUP FAILURE: postmaster alive"; exit 5
  fi
  LEFT=$(ls -A /tmp | grep -cE "pgdata|pgsock|dump|pg.log" || true)
  [ "$LEFT" = "0" ] || { echo "CLEANUP FAILURE: $LEFT leftovers in /tmp"; exit 5; }
  echo "stop+cleanup: OK (verified); wall time: $((T1-T0))s"
  echo SPIKE_OK
'
echo "--- canary checks (from root exec context) ---"
# The canary result is the trust-boundary evidence; its failure MUST fail the
# spike (review round 2, P1-19 remainder).
docker run --rm --user 0:0 --entrypoint bash "$IMAGE" -c '
  set -eu
  echo "root-secret" > /tmp/canary && chmod 600 /tmp/canary && chown 0:0 /tmp/canary
  if setpriv --reuid 10001 --regid 10001 --clear-groups cat /tmp/canary >/dev/null 2>&1; then
    echo "BOUNDARY VIOLATION: uid 10001 read root 0600 file" >&2
    exit 4
  fi
  if ! setpriv --reuid 10001 --regid 10001 --clear-groups true 2>/dev/null; then
    echo "SKIPPED: setpriv unavailable in this image" >&2
    exit 0
  fi
  echo "root 0600 canary unreadable by UID 10001: OK"
'
echo SPIKE2_DONE
