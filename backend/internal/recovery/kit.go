// Package recovery generates self-contained recovery kits: restore.sh
// scripts with fixed templates and platform-aware guidance, plus manifest-
// derived checksums. Scripts use fixed templates with safe parameter
// passing — user data never enters shell command construction, and every
// free-text field is sanitized to a safe charset before it reaches the
// template (phase-5 review: metadata must never be able to break out of a
// comment or quoted string).
package recovery

import (
	"fmt"
	"strconv"
	"strings"

	platformpkg "github.com/web-casa/supacove/backend/internal/platform"
)

// KitInput carries everything needed to generate a recovery kit.
type KitInput struct {
	BackupUUID       string
	Platform         platformpkg.Platform
	ArtifactFileName string
	SHA256           string
	KeyID            string
}

// sanitizeMeta reduces a template field to a fixed safe charset. All fields
// are system-generated today (job-N, hex fingerprints); the filter is a
// structural guarantee for any future caller that passes user-adjacent data.
func sanitizeMeta(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	out := b.String()
	if len(out) > 100 {
		out = out[:100]
	}
	if out == "" {
		out = "unknown"
	}
	return out
}

// restoreScriptTemplate generates a POSIX sh restore script.
//
// Safety properties (phase-5 review P1-01/02/03):
//   - NO line continuations anywhere: every command is a single line, so a
//     raw-string backslash can never turn an argument into a separate
//     command (P1-01 was exactly that failure).
//   - All work happens in an exclusive mktemp -d directory (0700); the
//     plaintext dump lives there at 0600 (umask 077) and the trap removes
//     ONLY that directory, on EXIT plus INT/TERM/HUP.
//   - The target conninfo must NOT carry an inline password (argv is
//     world-readable via ps); the password goes through the PGPASSWORD
//     environment variable, which libpq reads without any argv exposure.
//   - Pre-flight: tool presence, ciphertext existence, SHA-256 of the
//     ciphertext (error detection — not a signature), and target-emptiness
//     (a generic restore refuses non-empty targets; override only via
//     SUPABACKUP_ALLOW_NONEMPTY=1, e.g. for platform projects with managed
//     schemas — that path is NOT covered by the automated drill).
//   - The final psql count check is error-checked: a failed verification
//     exits non-zero with a PARTIAL-WRITE warning; nothing is swallowed.
//
// Profiles. A kit carries both restore paths and a default chosen at
// generation time (%[7]s); SUPABACKUP_PROFILE overrides it at run time.
//   - generic: the archive is restored as is.
//   - supabase: a full Supabase archive never restores cleanly as is (drill
//     of 2026-10-08, supabase/postgres 15.8), so this path checks up front
//     what such a restore needs — a superuser, and every extension of the
//     archive installable on the target — and restores from an edited
//     table of contents that leaves out the entries known to fail there.
//     pg_restore still runs with --exit-on-error: anything unexpected stops
//     the restore.
const restoreScriptTemplate = `#!/bin/sh
# ============================================================
# supabackup recovery kit
# Backup UUID: %[1]s
# Key ID:      %[2]s
# SHA-256:     %[3]s
# ============================================================
# HOW TO RUN
#   sh restore.sh '<target_conninfo>' /path/to/%[4]s
#   PGPASSWORD='<password>' must be exported for the target.
# Required tools: age, pg_restore, psql, sha256sum (or shasum).
%[5]sset -eu
umask 077

# Restore profile: "generic" or "supabase". This kit defaults to the one
# matching the backed-up database; SUPABACKUP_PROFILE overrides it.
PROFILE="${SUPABACKUP_PROFILE:-%[7]s}"
case "$PROFILE" in
  generic|supabase) : ;;
  *) echo "ERROR: SUPABACKUP_PROFILE must be 'generic' or 'supabase'." >&2; exit 2 ;;
esac

usage() {
  echo "Usage: sh restore.sh <target_conninfo_without_password> <encrypted_file>" >&2
  echo "Export the password via PGPASSWORD; never inline it in the connection string." >&2
  exit 2
}

TARGET_URL="${1:-}"
ENCRYPTED_FILE="${2:-}"
[ -n "$TARGET_URL" ] || usage
[ -n "$ENCRYPTED_FILE" ] || usage
AGE_IDENTITY_FILE="${AGE_IDENTITY_FILE:?Set AGE_IDENTITY_FILE to the offline age identity file}"

case "$TARGET_URL" in
  *%%*)
    echo "ERROR: percent-escaped characters are not accepted in the connection string." >&2
    echo "libpq decodes URI escapes (e.g. ?%%70assword=... is a password field), so an" >&2
    echo "encoded password would end up in the tool argv (readable via ps). Pass the" >&2
    echo "password via PGPASSWORD and use a plain, password-free connection string." >&2
    exit 2 ;;
esac
case "$TARGET_URL" in
  *://*:*@*|*[Pp][Aa][Ss][Ss][Ww][Oo][Rr][Dd]=*|*[Pp][Aa][Ss][Ss][Ww][Oo][Rr][Dd][[:space:]]*=*)
    echo "ERROR: the connection string appears to contain an inline password." >&2
    echo "libpq accepts 'password = ...' with spaces and any letter case," >&2
    echo "and passwords in argv are readable by every local user via ps. Use:" >&2
    echo "  PGPASSWORD='...' sh restore.sh <conninfo_without_password> <file>" >&2
    exit 2 ;;
esac
# URI query parameters are restricted to a TLS/timeout allowlist: libpq
# decodes percent-escapes in parameter NAMES too, so an allowlist over the
# raw (undecoded) key closes the encoded-password bypass.
QUERY="${TARGET_URL#*\?}"
if [ "$QUERY" != "$TARGET_URL" ]; then
  REST="$QUERY"
  while [ -n "$REST" ]; do
    PAIR="${REST%%%%&*}"
    case "$REST" in
      *"&"*) REST="${REST#*&}" ;;
      *) REST="" ;;
    esac
    KEY="${PAIR%%%%=*}"
    case "$KEY" in
      sslmode|sslcert|sslkey|sslrootcert|sslnegotiation|connect_timeout|application_name) : ;;
      *) echo "ERROR: unsupported connection parameter '$KEY'. Only TLS/timeout parameters" >&2
         echo "are accepted here; pass credentials via PGPASSWORD, never in the URI." >&2
         exit 2 ;;
    esac
  done
fi
case "$AGE_IDENTITY_FILE" in
  /*) : ;;
  *) echo "ERROR: AGE_IDENTITY_FILE must be an absolute path." >&2; exit 2 ;;
esac

for tool in age pg_restore psql; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool not found: $tool" >&2
    exit 3
  fi
done

if [ ! -f "$ENCRYPTED_FILE" ]; then
  echo "ERROR: encrypted backup file not found: $ENCRYPTED_FILE" >&2
  exit 1
fi

HASH_HEX=""
if command -v sha256sum >/dev/null 2>&1; then
  HASH_HEX=$(sha256sum "$ENCRYPTED_FILE" 2>/dev/null | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
  HASH_HEX=$(shasum -a 256 "$ENCRYPTED_FILE" 2>/dev/null | cut -d' ' -f1)
else
  echo "ERROR: no sha256 tool available (need sha256sum or shasum)." >&2
  exit 3
fi
if [ "$HASH_HEX" != "%[3]s" ]; then
  echo "ERROR: ciphertext SHA-256 mismatch (want %[3]s, got $HASH_HEX)." >&2
  echo "The encrypted file is not the backup this kit was generated for." >&2
  exit 1
fi

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/supabackup-restore.XXXXXX") || exit 3
chmod 700 "$WORK_DIR"
DUMP_FILE="$WORK_DIR/plaintext.dump"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

echo "[1/4] Checking target..."
PRE_TABLES=$(psql --dbname="$TARGET_URL" -X -v ON_ERROR_STOP=1 -At -c "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema','pg_toast')")
if [ -z "$PRE_TABLES" ]; then
  echo "ERROR: could not read the target database state (connection or permission problem)." >&2
  exit 1
fi
if [ "$PRE_TABLES" != "0" ] && [ "${SUPABACKUP_ALLOW_NONEMPTY:-0}" != "1" ]; then
  echo "ERROR: target database is NOT empty ($PRE_TABLES user tables)." >&2
  echo "A failed restore into a non-empty target can leave it PARTIALLY written." >&2
  echo "Restore into an empty database, or set SUPABACKUP_ALLOW_NONEMPTY=1" >&2
  echo "if you accept that risk (e.g. platform projects with managed schemas)." >&2
  exit 1
fi

if [ "$PROFILE" = "supabase" ]; then
  IS_SUPER=$(psql --dbname="$TARGET_URL" -X -v ON_ERROR_STOP=1 -At -c "SELECT rolsuper FROM pg_roles WHERE rolname = current_user") || {
    echo "ERROR: could not read the role of the target connection." >&2
    exit 1
  }
  if [ "$IS_SUPER" != "t" ]; then
    echo "ERROR: the supabase profile needs a SUPERUSER connection to the target." >&2
    echo "The archive holds rows for tables owned by Supabase's extensions (the" >&2
    echo "vault, for one), which an ordinary role cannot write. On the" >&2
    echo "supabase/postgres image connect as supabase_admin." >&2
    exit 1
  fi
fi

echo "[2/4] Decrypting..."
age --decrypt -i "$AGE_IDENTITY_FILE" -o "$DUMP_FILE" "$ENCRYPTED_FILE"
[ -s "$DUMP_FILE" ] || { echo "ERROR: decryption produced an empty file." >&2; exit 1; }

echo "[3/4] Restoring..."
if [ "$PROFILE" = "supabase" ]; then
  TOC_FILE="$WORK_DIR/toc.list"
  USE_FILE="$WORK_DIR/use.list"
  pg_restore --list "$DUMP_FILE" > "$TOC_FILE" || {
    echo "ERROR: could not read the archive's table of contents." >&2
    exit 1
  }
  AVAILABLE=$(psql --dbname="$TARGET_URL" -X -v ON_ERROR_STOP=1 -At -c "SELECT name FROM pg_available_extensions") || {
    echo "ERROR: could not list the extensions available on the target." >&2
    exit 1
  }
  NL='
'
  # The one function Supabase creates through an event trigger when
  # pg_graphql is installed. A plain archive holds the GRANT on it but not
  # the function, so that grant has nothing to attach to.
  GQL_SIG='graphql("operationName" text, query text, variables jsonb, extensions jsonb)'
  # A table-of-contents record is "<id>; <n> <n> <TYPE...> <names...>".
  # Object names are data from the backed-up database, so nothing is ever
  # matched inside a line: toc_record strips the numeric prefix and what is
  # compared is the START of the record, which is the type pg_restore wrote.
  toc_record() {
    RECORD=""
    T_ID="${1%%%%;*}"
    case "$T_ID" in ''|*[!0-9]*) return 1 ;; esac
    T_REST="${1#*; }"
    T_A="${T_REST%%%% *}"
    T_REST="${T_REST#* }"
    T_B="${T_REST%%%% *}"
    case "$T_A" in ''|*[!0-9]*) return 1 ;; esac
    case "$T_B" in ''|*[!0-9]*) return 1 ;; esac
    RECORD="${T_REST#* }"
  }
  # First pass: is that function itself in the archive? Then its grant is
  # an ordinary one and stays.
  HAS_GQL_FN=0
  while IFS= read -r LINE || [ -n "$LINE" ]; do
    toc_record "$LINE" || continue
    case "$RECORD" in
      "FUNCTION graphql_public $GQL_SIG "*) HAS_GQL_FN=1 ;;
    esac
  done < "$TOC_FILE"
  MISSING=""
  SKIPPED=0
  : > "$USE_FILE"
  while IFS= read -r LINE || [ -n "$LINE" ]; do
    if toc_record "$LINE"; then
      case "$RECORD" in
        "ACL graphql_public FUNCTION $GQL_SIG "*)
          if [ "$HAS_GQL_FN" = "0" ]; then
            SKIPPED=$((SKIPPED + 1))
            continue
          fi ;;
        "EXTENSION - "*)
          EXT="${RECORD#EXTENSION - }"
          EXT="${EXT%%%% *}"
          case "$NL$AVAILABLE$NL" in
            *"$NL$EXT$NL"*) : ;;
            *) MISSING="$MISSING $EXT" ;;
          esac ;;
      esac
    fi
    printf '%%s\n' "$LINE" >> "$USE_FILE"
  done < "$TOC_FILE"
  if [ -n "$MISSING" ]; then
    echo "ERROR: the target server cannot install these extensions of the archive:$MISSING" >&2
    echo "Nothing was written. Restore onto a server that has them — the" >&2
    echo "supabase/postgres image ships Supabase's own — or install them first." >&2
    exit 1
  fi
  echo "Supabase profile: $SKIPPED archive entries left out (the grant on graphql_public.graphql, a function Supabase creates itself)."
  if ! pg_restore --dbname="$TARGET_URL" --exit-on-error --no-owner --use-list="$USE_FILE" "$DUMP_FILE"; then
    echo "ERROR: pg_restore failed. The target may be PARTIALLY written;" >&2
    echo "drop the database and start from an empty target before retrying." >&2
    exit 1
  fi
elif ! pg_restore --dbname="$TARGET_URL" --exit-on-error --no-owner "$DUMP_FILE"; then
  echo "ERROR: pg_restore failed. The target may be PARTIALLY written;" >&2
  echo "drop the database and start from an empty target before retrying." >&2
  exit 1
fi

echo "[4/4] Verifying restored object count..."
POST_TABLES=$(psql --dbname="$TARGET_URL" -X -v ON_ERROR_STOP=1 -At -c "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema','pg_toast')") || {
  echo "ERROR: could not verify the restored tables. The target may be PARTIALLY written." >&2
  exit 1
}
echo "Restored $POST_TABLES user tables."
%[6]s
echo "Done. Decrypted plaintext was removed with the temporary directory."
`

// tablesCheckFor builds the manifest cross-check emitted after the count.
// expectedTables < 0 (unknown — older manifests) checks only that the count
// query succeeded and the result is numeric; no comparison is emitted.
func tablesCheckFor(expectedTables int64) string {
	parse := `case "$POST_TABLES" in
  ''|*[!0-9]*)
    echo "ERROR: table count query returned a non-numeric result; treat the restore as unverified." >&2
    exit 1 ;;
esac`
	if expectedTables < 0 {
		return parse
	}
	want := strconv.FormatInt(expectedTables, 10)
	return parse + `
if [ "$POST_TABLES" != "` + want + `" ]; then
  echo "ERROR: expected ` + want + ` user tables (per the manifest), got $POST_TABLES." >&2
  echo "The restore completed without SQL errors but the object set does not match; treat the backup as suspect." >&2
  exit 1
fi`
}

// GenerateRestoreScript returns a self-contained restore.sh script. The
// manifest table count is unknown here; prefer GenerateRestoreScriptWithTables.
func GenerateRestoreScript(in KitInput) string {
	return GenerateRestoreScriptWithTables(in, -1)
}

// GenerateRestoreScriptWithTables embeds the expected user-table count from
// the manifest so the script can cross-check the restore result.
func GenerateRestoreScriptWithTables(in KitInput, expectedTables int64) string {
	notes := platformpkg.RecoveryNotes(in.Platform)
	var noteLines strings.Builder
	for _, n := range notes {
		noteLines.WriteString("# ")
		noteLines.WriteString(n)
		noteLines.WriteString("\n")
	}
	if expectedTables >= 0 {
		noteLines.WriteString("# Expected user tables at dump time: ")
		noteLines.WriteString(strconv.FormatInt(expectedTables, 10))
		noteLines.WriteString(" (the script cross-checks the restore result).\n")
	}
	profile := "generic"
	if in.Platform == platformpkg.Supabase {
		profile = "supabase"
	}
	return fmt.Sprintf(restoreScriptTemplate,
		sanitizeMeta(in.BackupUUID),
		sanitizeMeta(in.KeyID),
		sanitizeMeta(in.SHA256),
		sanitizeMeta(in.ArtifactFileName),
		noteLines.String(),
		tablesCheckFor(expectedTables),
		profile)
}

// GenerateRecoveryGuide returns platform-specific recovery guidance text.
func GenerateRecoveryGuide(p platformpkg.Platform) string {
	notes := platformpkg.RecoveryNotes(p)
	return strings.Join(notes, "\n")
}
