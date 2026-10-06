#!/bin/sh
# E2E against the FINAL embedded artifact: builds the console into the Go
# binary, boots it on a free port with a throwaway data dir, bootstraps an
# admin, and runs the Playwright suite against it.
set -eu
cd "$(dirname "$0")/.."

PORT="${SB_E2E_PORT:-36470}"
DATA="$(mktemp -d /tmp/sb-e2e.XXXXXX)"
trap 'kill "$PID" 2>/dev/null || true; rm -rf "$DATA"' EXIT

make frontend backend
SB_DATA_DIR="$DATA" SB_ADDR="127.0.0.1:$PORT" ./bin/supabackup serve &
PID=$!
READY=0
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$PORT/api/ready" >/dev/null; then READY=1; break; fi
  sleep 0.5
done
[ "$READY" = 1 ] || { echo "server never became ready on :$PORT" >&2; exit 1; }
# The CLI prints prose around the token; extract the indented token line.
TOKEN="$(SB_DATA_DIR="$DATA" ./bin/supabackup bootstrap | awk 'NF==1 && length($0)>20 { sub(/^ +/, ""); sub(/ +$/, ""); print; exit }')"

cd frontend
SB_E2E_BASE_URL="http://127.0.0.1:$PORT" \
SB_E2E_TOKEN="$TOKEN" \
SB_E2E_ADMIN=e2e-admin \
SB_E2E_PASSWORD='E2e-Password-1234' \
SB_E2E_CHROMIUM="${SB_E2E_CHROMIUM:-}" \
npx playwright test
