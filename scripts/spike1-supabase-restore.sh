#!/usr/bin/env bash
# Spike 1: validate a Supabase export/restore profile between two REAL Supabase
# projects. See docs/adr/ADR-003-supabase-recovery-profile.md.
# Requires: psql, pg_dump, pg_restore (client versions 15+) and both URLs.
set -euo pipefail

: "${SOURCE_DB_URL:?set SOURCE_DB_URL}"
: "${TARGET_DB_URL:?set TARGET_DB_URL}"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT
step() { printf '\n=== %s ===\n' "$*"; }

step "0. connectivity + versions"
psql "$SOURCE_DB_URL" -tAc "SELECT version();" | head -1
psql "$TARGET_DB_URL" -tAc "SELECT version();" | head -1

step "1. inventory of source (schemas, roles referenced, extensions)"
psql "$SOURCE_DB_URL" -tAc "SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' ORDER BY 1;"
psql "$SOURCE_DB_URL" -tAc "SELECT extname FROM pg_extension ORDER BY 1;"

step "P-B: single plain -Fc dump (expected to FAIL on restore — control baseline)"
pg_dump "$SOURCE_DB_URL" -Fc -f "$OUT/plain.dump"
pg_restore "$TARGET_DB_URL" --exit-on-error "$OUT/plain.dump" && \
  echo "P-B RESULT: PASS (unexpected)" || echo "P-B RESULT: FAIL (expected)"

step "P-C: application-schemas-only dump"
USER_SCHEMAS=$(psql "$SOURCE_DB_URL" -tAc \
  "SELECT string_agg(quote_literal(nspname), ',') FROM pg_namespace
   WHERE nspname NOT LIKE 'pg_%'
     AND nspname NOT IN ('information_schema','auth','storage','realtime','vault','supabase_functions','extensions','graphql','graphql_public','pgbouncer','net','pgsodium','pgsodium_masks','pgtle','supabase_storage')")
[ -n "$USER_SCHEMAS" ] || { echo "no user schemas found"; exit 1; }
pg_dump "$SOURCE_DB_URL" -Fc $(eval "echo -n $(psql "$SOURCE_DB_URL" -tAc \
  "SELECT string_agg('-n ' || quote_literal(nspname), ' ') FROM pg_namespace
   WHERE nspname NOT LIKE 'pg_%'
     AND nspname NOT IN ('information_schema','auth','storage','realtime','vault','supabase_functions','extensions','graphql','graphql_public','pgbouncer','net','pgsodium','pgsodium_masks','pgtle','supabase_storage')")") \
  -f "$OUT/app.dump"
pg_restore "$TARGET_DB_URL" --exit-on-error --no-owner "$OUT/app.dump" && \
  echo "P-C RESULT: PASS" || echo "P-C RESULT: FAIL"

step "P-A: official three-part dump (roles + schema + data)"
# Roles: supabase manages most roles; capture custom ones (not reserved).
pg_dumpall --connection "$SOURCE_DB_URL" --roles-only > "$OUT/roles.sql" 2>/dev/null || \
  pg_dump "$SOURCE_DB_URL" --roles-only > "$OUT/roles.sql" 2>/dev/null || \
  echo "roles dump not available with this client"
psql "$SOURCE_DB_URL" -tAc "SELECT rolname FROM pg_roles
  WHERE rolname NOT LIKE 'pg_%' AND rolname NOT IN ('postgres','anon','authenticated','service_role','authenticator','supabase_auth_admin','supabase_storage_admin','storage_admin','dashboard_user','supabase_admin','supabase_read_only_user','supabase_realtime_admin','pgbouncer','pg_database_owner')" > "$OUT/custom_roles.txt"
cat "$OUT/custom_roles.txt" || true

psql "$TARGET_DB_URL" -qc "DO \$\$ DECLARE r record; BEGIN
  FOR r IN SELECT rolname FROM unnest(ARRAY[$(psql "$SOURCE_DB_URL" -tAc \
    "SELECT string_agg(quote_literal(rolname), ',') FROM pg_roles WHERE rolname NOT LIKE 'pg_%' AND rolname NOT IN ('postgres','anon','authenticated','service_role','authenticator','supabase_auth_admin','supabase_storage_admin','storage_admin','dashboard_user','supabase_admin','supabase_read_only_user','supabase_realtime_admin','pgbouncer','pg_database_owner')")] ) AS rolname
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r.rolname) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', r.rolname);
      RAISE NOTICE 'created role %', r.rolname;
    END IF;
  END LOOP; END \$\$;" && echo "custom roles staged in target"

pg_dump "$SOURCE_DB_URL" -Fc --schema-only \
  $(psql "$SOURCE_DB_URL" -tAc \
    "SELECT string_agg('-N ' || quote_literal(nspname), ' ') FROM pg_namespace
     WHERE nspname IN ('auth','storage','realtime','vault','supabase_functions','supabase_storage','net','pgsodium','pgsodium_masks','pgtle')") \
  -f "$OUT/schema.dump"
pg_restore "$TARGET_DB_URL" --exit-on-error --no-owner --schema-only "$OUT/schema.dump" && \
  echo "P-A schema RESULT: PASS" || echo "P-A schema RESULT: FAIL"

pg_dump "$SOURCE_DB_URL" -Fc --data-only \
  $(psql "$SOURCE_DB_URL" -tAc \
    "SELECT string_agg('-n ' || quote_literal(nspname), ' ') FROM pg_namespace
     WHERE nspname NOT LIKE 'pg_%'
       AND nspname NOT IN ('information_schema','auth','storage','realtime','vault','supabase_functions','extensions','graphql','graphql_public','pgbouncer','net','pgsodium','pgsodium_masks','pgtle','supabase_storage')") \
  -f "$OUT/data.dump"
pg_restore "$TARGET_DB_URL" --exit-on-error --no-owner --data-only "$OUT/data.dump" && \
  echo "P-A data RESULT: PASS" || echo "P-A data RESULT: FAIL"

step "verification: row counts of user tables in source vs target"
psql "$SOURCE_DB_URL" -tAc "SELECT schemaname, relname, n_live_tup FROM pg_stat_user_tables ORDER BY 1,2" > "$OUT/src_counts.txt"
psql "$TARGET_DB_URL" -tAc "SELECT schemaname, relname, n_live_tup FROM pg_stat_user_tables ORDER BY 1,2" > "$OUT/tgt_counts.txt"
diff "$OUT/src_counts.txt" "$OUT/tgt_counts.txt" && echo "counts match (approximate)" || \
  echo "counts differ — review manually (statistics are estimates)"

step "DONE — record all results into docs/adr/ADR-003-supabase-recovery-profile.md"
