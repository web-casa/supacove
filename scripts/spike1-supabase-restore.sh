#!/usr/bin/env bash
# Spike 1: validate a Supabase export/restore profile between two REAL Supabase
# projects. See docs/adr/ADR-003-supabase-recovery-profile.md.
#
# Exit code: 0 only if at least one candidate profile restores cleanly
# (pg_restore --exit-on-error) and the recorded checks pass. A failed control
# baseline (P-B) is expected and does not fail the run; a failed CANDIDATE
# profile is recorded and makes the final exit code non-zero.
#
# Requirements: psql, pg_dump, pg_restore, pg_dumpall (client 15+).
# Security: connection URIs are passed via PGPASSFILE-free .pgpass-style files
# is out of scope for a spike; avoid exporting URLs into shared shells.
set -uo pipefail

: "${SOURCE_DB_URL:?set SOURCE_DB_URL}"
: "${TARGET_DB_URL:?set TARGET_DB_URL}"
OUT="${OUT:-$(mktemp -d)}"
echo "artifacts (kept): $OUT" >&2
FAILED_CANDIDATES=0
PASSED_CANDIDATES=0
step() { printf '\n=== %s ===\n' "$*"; }

# Restore with the mandatory --dbname form; returns pg_restore's exit code.
# Each candidate's stderr goes to its own file when restore_stderr is set.
restore() { # $1=target url  $2=archive  rest=extra flags
  local target="$1" archive="$2"
  shift 2
  local err="${restore_stderr:-$OUT/restore-unspecified.log}"
  pg_restore --dbname="$target" --exit-on-error "$@" "$archive" 2>"$err"
}

step "0. connectivity + versions"
psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -tAc "SELECT version();" | head -1 || exit 1
psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -tAc "SELECT version();" | head -1 || exit 1

step "1. inventory of source (schemas, extensions)"
psql --dbname="$SOURCE_DB_URL" -tAc \
  "SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' ORDER BY 1;" \
  | tee "$OUT/src-schemas.txt"
psql --dbname="$SOURCE_DB_URL" -tAc "SELECT extname FROM pg_extension ORDER BY 1;" \
  | tee "$OUT/src-extensions.txt"

# User schemas (everything except platform/Postgres namespaces), read into a
# NUL-separated array so spaces and quotes survive (review P1-18).
psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -At -0 -c \
  "SELECT nspname FROM pg_namespace
   WHERE nspname NOT LIKE 'pg\_%'
     AND nspname NOT IN ('information_schema','auth','storage','realtime','vault',
                         'supabase_functions','extensions','graphql','graphql_public',
                         'pgbouncer','net','pgsodium','pgsodium_masks','pgtle',
                         'supabase_storage','supabase_db_org_0000')
   ORDER BY 1" > "$OUT/schemas.nul" \
  || { echo "FATAL: schema enumeration failed" >&2; exit 1; }
mapfile -d '' USER_SCHEMAS < "$OUT/schemas.nul"
echo "user schemas: ${USER_SCHEMAS[*]-none}" >&2
if [ "${#USER_SCHEMAS[@]}" -eq 0 ]; then
  echo "FATAL: no user schemas found — wrong source project?" >&2
  exit 1
fi

# pg_dump -n/-N take psql patterns (case folding, *, ?, regex chars) — not
# LIKE. Double-quoting a pattern makes it literal; inner quotes double
# (review round 2 P1-18, corrected in round 5 per psql PATTERNS docs).
pattern_literal() { # $1 = exact name -> quoted psql pattern
  local q="${1//\"/\"\"}"
  printf '"%s"' "$q"
}

schema_flags() { # $1 = -n or -N
  local flag="$1"
  local args=()
  for s in "${USER_SCHEMAS[@]}"; do
    args+=("$flag" "$(pattern_literal "$s")")
  done
  printf '%s\0' "${args[@]}"
}

step "P-B: single plain -Fc dump (control baseline; expected to FAIL)"
pg_dump --dbname="$SOURCE_DB_URL" -Fc -f "$OUT/plain.dump"
if restore_stderr="$OUT/restore-P-B.log" restore "$TARGET_DB_URL" "$OUT/plain.dump"; then
  echo "P-B RESULT: PASS (unexpected — investigate)"
  PASSED_CANDIDATES=$((PASSED_CANDIDATES+1))
else
  echo "P-B RESULT: FAIL (expected; stderr in $OUT/restore-P-B.log)"
fi

step "P-C: application-schemas-only dump (candidate 1)"
dump_args=()
while IFS= read -r -d '' arg; do dump_args+=("$arg"); done < <(schema_flags -n)
pg_dump --dbname="$SOURCE_DB_URL" -Fc "${dump_args[@]}" -f "$OUT/app.dump" \
  && echo "P-C dump: OK ($(du -h "$OUT/app.dump" | cut -f1))" \
  || { echo "P-C dump: FAILED"; FAILED_CANDIDATES=$((FAILED_CANDIDATES+1)); }
if restore_stderr="$OUT/restore-P-C.log" restore "$TARGET_DB_URL" "$OUT/app.dump" --no-owner; then
  echo "P-C RESULT: PASS"
  PASSED_CANDIDATES=$((PASSED_CANDIDATES+1))
else
  echo "P-C RESULT: FAIL (stderr: $OUT/restore-P-C.log)"
  FAILED_CANDIDATES=$((FAILED_CANDIDATES+1))
fi

step "P-A: official three-part dump (candidate 2)"
# Custom roles (not Supabase-reserved), exported with pg_dumpall --dbname.
psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -At -0 -c \
  "SELECT rolname FROM pg_roles
   WHERE rolname NOT LIKE 'pg\_%'
     AND rolname NOT IN ('postgres','anon','authenticated','service_role','authenticator',
                         'supabase_auth_admin','supabase_storage_admin','storage_admin',
                         'dashboard_user','supabase_admin','supabase_read_only_user',
                         'supabase_realtime_admin','pgbouncer','pg_database_owner')" \
  > "$OUT/roles.nul" || { echo "FATAL: role enumeration failed" >&2; exit 1; }
mapfile -d '' CUSTOM_ROLES < "$OUT/roles.nul"
if [ "${#CUSTOM_ROLES[@]}" -gt 0 ]; then
  echo "custom roles: ${CUSTOM_ROLES[*]}" >&2
  pg_dumpall --dbname="$SOURCE_DB_URL" --roles-only > "$OUT/roles.sql" 2>/dev/null \
    || echo "note: full roles dump unavailable; staging NOLOGIN placeholders only" >&2
  for r in "${CUSTOM_ROLES[@]}"; do
    # Two-step, server-side quoting only: check existence, then generate and
    # execute the DDL. Never interpolate the name into SQL text (R2-P1-04);
    # single-layer format so quoting cannot break (R3-P1-03). All psql calls
    # use ON_ERROR_STOP.
    exists=$(printf "SELECT CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role') THEN 1 ELSE 0 END;" \
      | psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -At -v role="$r") \
      || { echo "role existence check failed: $r" >&2; exit 1; }
    case "$exists" in
      1) echo "role already present: $r" >&2 ;;
      0)
        ddl=$(printf "SELECT format('CREATE ROLE %%I NOLOGIN', :'role');" \
          | psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -At -v role="$r") \
          || { echo "role quoting failed: $r" >&2; exit 1; }
        psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -qc "$ddl" \
          || { echo "role create failed: $r" >&2; exit 1; }
        # Verify the role actually exists now (never trust exit codes alone).
        verify=$(printf "SELECT CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role') THEN 1 ELSE 0 END;" \
          | psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -At -v role="$r") \
          || { echo "role verify failed: $r" >&2; exit 1; }
        [ "$verify" = "1" ] || { echo "role NOT created despite success exit: $r" >&2; exit 1; }
        ;;
      *) echo "unexpected existence result for role $r: $exists" >&2; exit 1 ;;
    esac
  done
fi

exclude_platform=()
while IFS= read -r -d '' arg; do exclude_platform+=("$arg"); done < <(
  for s in auth storage realtime vault supabase_functions supabase_storage net pgsodium pgsodium_masks pgtle; do
    printf -- '--exclude-schema\0%s\0' "$s"
  done)

pg_dump --dbname="$SOURCE_DB_URL" -Fc --schema-only "${exclude_platform[@]}" \
  -f "$OUT/schema.dump" && echo "P-A schema dump: OK" || echo "P-A schema dump: FAILED"
if restore_stderr="$OUT/restore-P-A-schema.log" restore "$TARGET_DB_URL" "$OUT/schema.dump" --no-owner; then
  echo "P-A schema RESULT: PASS"
  PASSED_CANDIDATES=$((PASSED_CANDIDATES+1))
else
  echo "P-A schema RESULT: FAIL (stderr: $OUT/restore-P-A-schema.log)"
  FAILED_CANDIDATES=$((FAILED_CANDIDATES+1))
fi

dump_args=()
while IFS= read -r -d '' arg; do dump_args+=("$arg"); done < <(schema_flags -n)
pg_dump --dbname="$SOURCE_DB_URL" -Fc --data-only "${dump_args[@]}" \
  -f "$OUT/data.dump" && echo "P-A data dump: OK" || echo "P-A data dump: FAILED"
if restore_stderr="$OUT/restore-P-A-data.log" restore "$TARGET_DB_URL" "$OUT/data.dump" --no-owner --disable-triggers; then
  echo "P-A data RESULT: PASS"
  PASSED_CANDIDATES=$((PASSED_CANDIDATES+1))
else
  echo "P-A data RESULT: FAIL (stderr: $OUT/restore-P-A-data.log)"
  FAILED_CANDIDATES=$((FAILED_CANDIDATES+1))
fi

step "verification: exact row counts of user tables (source vs target)"
# The LATERAL above is intentionally simplified; exact per-table counting is
# done by the loop below (correctness over cleverness).
: > "$OUT/src-counts.txt"
: > "$OUT/tgt-counts.txt"
COUNT_ERROR=0
SCHEMA_IDX=0
for s in "${USER_SCHEMAS[@]}"; do
  SCHEMA_IDX=$((SCHEMA_IDX+1))
  # Table list: schema name bound as a psql variable, quoted server-side.
  # Enumeration failure is an error, never an empty-but-successful result
  # (review round 4, P1-18).
  printf "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = :'sch' AND c.relkind = 'r' ORDER BY 1;" \
    | psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -At -0 -v sch="$s" > "$OUT/tables-$SCHEMA_IDX.nul" \
    || { echo "FATAL: table enumeration failed for schema $s" >&2; exit 1; }
  mapfile -d '' TABLES < "$OUT/tables-$SCHEMA_IDX.nul"
  if [ "${#TABLES[@]}" -eq 0 ]; then
    echo "note: schema $s has no tables" >&2
    continue
  fi
  for tbl in "${TABLES[@]}"; do
    [ -n "$tbl" ] || continue
    count_sql=$(printf "SELECT format('SELECT count(*) FROM %%I.%%I', :'sch', :'tbl');" \
      | psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -At -v sch="$s" -v tbl="$tbl") \
      || { echo "FATAL: quote generation failed: $s.$tbl" >&2; exit 1; }
    src_n=$(psql --dbname="$SOURCE_DB_URL" --set=ON_ERROR_STOP=1 -At -c "$count_sql" 2>>"$OUT/count-errors.log")
    src_rc=$?
    tgt_n=$(psql --dbname="$TARGET_DB_URL" --set=ON_ERROR_STOP=1 -At -c "$count_sql" 2>>"$OUT/count-errors.log")
    tgt_rc=$?
    # A failed count is recorded in a dedicated error column — the VALUE
    # channel never carries failure markers (review round 4, R4-P2-01).
    if [ $src_rc -ne 0 ] || [ $tgt_rc -ne 0 ]; then
      echo "$s.$tbl:COUNT_ERROR" >> "$OUT/src-counts.txt"
      echo "$s.$tbl:COUNT_ERROR" >> "$OUT/tgt-counts.txt"
      echo "count FAILED: $s.$tbl" >&2
      COUNT_ERROR=1
      continue
    fi
    echo "$s.$tbl:$src_n" >> "$OUT/src-counts.txt"
    echo "$s.$tbl:$tgt_n" >> "$OUT/tgt-counts.txt"
  done
done
[ "$COUNT_ERROR" -eq 0 ] || FAILED_CANDIDATES=$((FAILED_CANDIDATES+1))
if diff -u "$OUT/src-counts.txt" "$OUT/tgt-counts.txt"; then
  echo "exact row counts match"
else
  echo "ROW COUNT MISMATCH — see diff above"
  FAILED_CANDIDATES=$((FAILED_CANDIDATES+1))
fi

step "summary"
echo "candidates passed: $PASSED_CANDIDATES; failed: $FAILED_CANDIDATES"
echo "Record profile outcomes, restore stderr, TOC and this summary into docs/adr/ADR-003-supabase-recovery-profile.md"
[ "$PASSED_CANDIDATES" -gt 0 ] && [ "$FAILED_CANDIDATES" -eq 0 ]
