#!/usr/bin/env bash
# Capacity benchmark (dev-plan Phase 8, P1-13): measures the backup pipeline
# phases against a REAL PostgreSQL container with a low-compression fixture:
#   seed → pg_dump (host client) → age encrypt → embedded-instance restore
# with wall times, sizes and peak RSS per phase. Results feed docs/capacity.md.
# Requirements on the host: docker, pg_dump 18, age, /usr/bin/time, initdb/pg_ctl.
set -euo pipefail

ROWS="${ROWS:-2000000}"            # fixture rows (random payloads = incompressible)
PAYLOAD="${PAYLOAD:-180}"          # payload bytes per row
PGIMAGE="${PGIMAGE:-postgres:18-alpine}"
WORK=$(mktemp -d /tmp/sb-capacity.XXXXXX)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true' EXIT
CONTAINER="sb-capacity-$(date +%s)"

echo "== supacove capacity benchmark =="
echo "rows=$ROWS payload_bytes=$PAYLOAD workdir=$WORK"

# 1) Throwaway PostgreSQL on a random loopback port.
docker run -d --name "$CONTAINER" -e POSTGRES_PASSWORD=cap -e POSTGRES_DB=capdb \
  -p 127.0.0.1::5432 "$PGIMAGE" >/dev/null
PORT=$(docker port "$CONTAINER" 5432 | head -1 | awk -F: '{print $NF}')
URI="postgresql://postgres:cap@127.0.0.1:${PORT}/capdb?sslmode=disable"
for _ in $(seq 60); do
  if pg_isready -h 127.0.0.1 -p "$PORT" >/dev/null 2>&1; then break; fi
  sleep 1
done
echo "container ready on port $PORT"

# 2) Random hex TEXT fixture: true random bytes hex-encoded. zlib still
#    compresses hex somewhat (measured ratio ≈0.57) — it is NOT the worst
#    case; use bytea for a truly incompressible payload.
echo "== seeding $ROWS rows =="
SEED_T0=$(date +%s)
docker exec "$CONTAINER" psql -U postgres -d capdb -v ON_ERROR_STOP=1 -c \
  "CREATE EXTENSION IF NOT EXISTS pgcrypto;
   CREATE TABLE bench (id bigserial PRIMARY KEY, payload text, created timestamptz DEFAULT now());
   INSERT INTO bench (payload) SELECT encode(gen_random_bytes($PAYLOAD), 'hex') FROM generate_series(1, $ROWS);" \
  > /dev/null || { echo "seed FAILED"; exit 1; }
echo "seed=$(( $(date +%s) - SEED_T0 ))s"

# phase runs a pipeline stage, printing wall seconds and peak RSS (from
# /proc/<pid>/status VmHWM — the same figure /usr/bin/time -v would report).
# /usr/bin/time is NOT assumed (minimal containers lack it).
phase() {
  local label="$1"; shift
  local t0
  t0=$(date +%s%N)
  "$@" >"$WORK/$label.out" 2>"$WORK/$label.err" &
  local pid=$!
  local peak=0
  local kb
  while kill -0 "$pid" 2>/dev/null; do
    kb=$(awk '/VmHWM/{print $2}' "/proc/$pid/status" 2>/dev/null || echo 0)
    [ "${kb:-0}" -gt "$peak" ] && peak=$kb
    sleep 0.2
  done
  wait "$pid"; local rc=$?
  local ms=$(( ($(date +%s%N) - t0) / 1000000 ))
  if [ $rc -ne 0 ]; then
    echo "$label FAILED (rc=$rc):"; tail -3 "$WORK/$label.err"; exit 1
  fi
  echo "$label=${ms}ms maxrss_kb=$peak"
}

# 3) pg_dump (host client, custom format = zlib compression).
echo "== pg_dump =="
phase dump pg_dump --format=custom --file="$WORK/bench.dump" "$URI"

# 4) age encryption with an X25519 identity (exactly the product's shape).
AGE_FILE="$WORK/bench.dump.age"
age-keygen -o "$WORK/identity.txt" >/dev/null 2>&1
echo "== age encrypt =="
phase encrypt age --encrypt -i "$WORK/identity.txt" -o "$AGE_FILE" "$WORK/bench.dump"

# 5) Embedded-instance restore (the verifier's core phases: initdb →
#    start → pg_restore --exit-on-error → stop). The start/stop steps are
#    NOT part of the timed restore figure.
echo "== embedded restore =="
VDIR="$WORK/verify"; mkdir -p "$VDIR/data" "$VDIR/sock"
phase initdb /usr/lib/postgresql/18/bin/initdb -D "$VDIR/data" -A trust -U verifier
/usr/lib/postgresql/18/bin/pg_ctl -D "$VDIR/data" -l "$VDIR/pg.log" -o \
  "-c listen_addresses= -c unix_socket_directories=$VDIR/sock -c fsync=off -c synchronous_commit=off" \
  -w start >/dev/null
phase restore /usr/lib/postgresql/18/bin/pg_restore \
  --dbname="host=$VDIR/sock user=verifier dbname=postgres" --exit-on-error --no-owner "$WORK/bench.dump"
/usr/lib/postgresql/18/bin/pg_ctl -D "$VDIR/data" -m fast -w stop >/dev/null

echo "== sizes =="
DUMP_MB=$(du -m "$WORK/bench.dump" | cut -f1)
AGE_MB=$(du -m "$AGE_FILE" | cut -f1)
PHYS=$(docker exec "$CONTAINER" psql -U postgres -d capdb -At -c "SELECT pg_size_pretty(pg_database_size('capdb'))")
PHYS_BYTES=$(docker exec "$CONTAINER" psql -U postgres -d capdb -At -c "SELECT pg_database_size('capdb')")
echo "source_db_bytes=$PHYS_BYTES ($PHYS)"
echo "dump_archive_mb=$DUMP_MB"
echo "age_ciphertext_mb=$AGE_MB"
echo "== done: results above feed docs/capacity.md =="
