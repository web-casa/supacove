#!/usr/bin/env bash
# Spike 2 / ADR-004: can the RELEASE runtime shape (Debian slim, non-root
# UID 10001, no Docker socket, no privileges) run an embedded throwaway
# PostgreSQL for automatic restore verification?
#
# Usage: scripts/spike2-embedded-pg.sh [image]
#   default image: supabackup:runtime-spike (built with
#   `docker build --target runtime-spike -t supabackup:runtime-spike .`)
#
# Evidence (kept): $OUT holds the inner script, the truncated-archive stderr
# and a summary. The canary (root-0600 unreadable by UID 10001) MUST pass;
# otherwise the spike reports INCOMPLETE and exits non-zero.
set -euo pipefail
IMAGE="${1:-supabackup:runtime-spike}"
ROWS="${ROWS:-100000}"
OUT="${OUT:-$(mktemp -d)}"
mkdir -p "$OUT"
# The container's UID 10001 must traverse this directory (mktemp -d is 0700).
chmod 777 "$OUT"
chmod 644 "$OUT"/*.sh 2>/dev/null || true
echo "artifacts (kept): $OUT" >&2

# ---------------------------------------------------------------------------
# Inner script: runs inside the container as UID 10001.
# ---------------------------------------------------------------------------
cat > "$OUT/inner.sh" <<'INNER'
set -eu
export PATH="/usr/lib/postgresql/18/bin:$PATH"
echo "identity: $(id -u):$(id -g)"

mkdir -p /tmp/pgdata /tmp/pgsock
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

psql -h /tmp/pgsock -U verifier -d postgres -q -v ON_ERROR_STOP=1 \
  -c "CREATE TABLE spike(id int primary key, t text);
      INSERT INTO spike SELECT g, md5(g::text) FROM generate_series(1, $ROWS_INT) g;"
pg_dump -h /tmp/pgsock -U verifier -Fc -f /tmp/dump.dump postgres
echo "pg_dump -Fc: OK ($(du -h /tmp/dump.dump | cut -f1))"

psql -h /tmp/pgsock -U verifier -d postgres -qc "DROP TABLE spike;"
# Peak RSS for the restore client AND the postmaster. The postmaster PID is
# read from postmaster.pid: pgrep -f would match this script's own argv,
# which contains the same text (review round 5, R5-P2-02).
PM=$(head -1 /tmp/pgdata/postmaster.pid 2>/dev/null || true)
case "$PM" in ""|*[!0-9]*) PM="" ;; esac
pg_restore -h /tmp/pgsock -U verifier -d postgres --exit-on-error /tmp/dump.dump &
RP=$!
PEAK=0
PMPEAK=0
while kill -0 $RP 2>/dev/null; do
  if [ -r "/proc/$RP/status" ]; then
    KB=$(grep VmHWM "/proc/$RP/status" | tr -s " " | cut -d " " -f 2)
    case "$KB" in ""|*[!0-9]*) KB=0 ;; esac
    [ "$KB" -gt "$PEAK" ] && PEAK=$KB
  fi
  if [ -n "$PM" ] && [ -r "/proc/$PM/status" ]; then
    KB=$(grep VmHWM "/proc/$PM/status" | tr -s " " | cut -d " " -f 2)
    case "$KB" in ""|*[!0-9]*) KB=0 ;; esac
    [ "$KB" -gt "$PMPEAK" ] && PMPEAK=$KB
  fi
  sleep 0.05
done
wait $RP
if [ "$PEAK" -le 0 ] || [ "$PMPEAK" -le 0 ]; then
  echo "ERROR: RSS sampling failed (client=$PEAK postmaster=$PMPEAK KiB)" >&2
  exit 6
fi
N=$(psql -h /tmp/pgsock -U verifier -d postgres -tAc "SELECT COUNT(*) FROM spike;")
echo "pg_restore --exit-on-error: OK (rows=$N; client peak RSS ≈ $((PEAK/1024)) MiB, postmaster ≈ $((PMPEAK/1024)) MiB)"
[ "$N" = "$ROWS_INT" ] || { echo "ROW MISMATCH" >&2; exit 2; }

# Corruption detection on a CLEAN target: drop what the restore created so an
# "object already exists" error cannot masquerade as rejection. Truncation
# adapts to the archive size; rejection stderr is preserved as evidence
# (review rounds 2/4/5, P1-19).
psql -h /tmp/pgsock -U verifier -d postgres -qc "DROP TABLE IF EXISTS spike;"
SZ=$(wc -c < /tmp/dump.dump)
HALF=$((SZ / 2)); [ "$HALF" -gt 0 ] || HALF=1
head -c "$HALF" /tmp/dump.dump > /tmp/truncated.dump
if pg_restore -h /tmp/pgsock -U verifier -d postgres --exit-on-error /tmp/truncated.dump 2>/tmp/truncated-error.log; then
  echo "ERROR: truncated dump restored cleanly" >&2
  exit 3
fi
cp /tmp/truncated-error.log /spike-out/truncated-error.log
echo "truncated archive rejected: OK ($(wc -c < /tmp/truncated.dump) of $SZ bytes; reason: $(head -1 /tmp/truncated-error.log))"

pg_ctl -D /tmp/pgdata -m fast -w stop >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
  pgrep -f "[p]ostgres.*pgdata" >/dev/null 2>&1 || break
  sleep 0.5
done
rm -rf /tmp/pgdata /tmp/pgsock /tmp/dump.dump /tmp/truncated.dump /tmp/pg.log
T1=$(date +%s)
if pgrep -af "[p]ostgres" | grep -v pgrep >/dev/null 2>&1; then
  echo "CLEANUP FAILURE: postmaster alive" >&2
  exit 5
fi
LEFT=$(ls -A /tmp | grep -cE "pgdata|pgsock|dump|pg.log" || true)
[ "$LEFT" = "0" ] || { echo "CLEANUP FAILURE: $LEFT leftovers in /tmp" >&2; exit 5; }
echo "stop+cleanup: OK (verified); wall time: $((T1-T0))s"
echo SPIKE_OK
INNER

# Rows count is injected as a plain integer (already validated numeric).
case "$ROWS" in ""|*[!0-9]*) echo "ROWS must be an integer" >&2; exit 1 ;; esac
sed -i "3i ROWS_INT=$ROWS" "$OUT/inner.sh"
grep -q "^ROWS_INT=$ROWS$" "$OUT/inner.sh" || { echo "internal error: ROWS_INT injection failed" >&2; exit 1; }

echo "Running embedded-PG spike in [$IMAGE] as UID 10001..."
docker run --rm --entrypoint bash \
  -v "$OUT:/spike-out" \
  "$IMAGE" "/spike-out/inner.sh"

echo "--- canary checks (from root exec context) ---"
cat > "$OUT/canary.sh" <<'CANARY'
set -eu
echo "root-secret" > /tmp/canary && chmod 600 /tmp/canary && chown 0:0 /tmp/canary
if ! setpriv --reuid 10001 --regid 10001 --clear-groups true 2>/dev/null; then
  echo "INCOMPLETE: setpriv unavailable — canary NOT verified" >&2
  exit 7
fi
if setpriv --reuid 10001 --regid 10001 --clear-groups cat /tmp/canary >/dev/null 2>&1; then
  echo "BOUNDARY VIOLATION: uid 10001 read root 0600 file" >&2
  exit 4
fi
echo "root 0600 canary unreadable by UID 10001: OK"
CANARY
if ! docker run --rm --user 0:0 --entrypoint bash -v "$OUT:/spike-out" \
    "$IMAGE" "/spike-out/canary.sh"; then
  # Any canary failure (violation OR inability to verify) makes the spike
  # incomplete — never a done state (review round 5, P1-19).
  echo "SPIKE2_INCOMPLETE (canary failed)" >&2
  exit 1
fi
echo SPIKE2_DONE
