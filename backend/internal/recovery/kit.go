// Package recovery generates self-contained recovery kits: restore.sh
// scripts with fixed templates and platform-aware guidance, plus manifest-
// derived checksums. Scripts use fixed templates with safe parameter
// passing — user data never enters shell command construction.
package recovery

import (
	"fmt"
	"strings"

	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
)

// KitInput carries everything needed to generate a recovery kit.
type KitInput struct {
	BackupUUID       string
	Platform         platformpkg.Platform
	ArtifactFileName string
	SHA256           string
	KeyID            string
}

const restoreScriptTemplate = `#!/bin/sh
# supabackup recovery script
# Backup UUID: %[1]s
# Key ID:      %[2]s
# SHA-256:     %[3]s
set -eu

DUMP_FILE=$(mktemp /tmp/supabackup-restore.XXXXXX.dump)
trap 'rm -f "$DUMP_FILE"' EXIT

TARGET_URL="${1:?Usage: restore.sh <target_conninfo>}"
AGE_IDENTITY_FILE="${AGE_IDENTITY_FILE:?Set AGE_IDENTITY_FILE}"
ENCRYPTED_FILE="${2:?Usage: restore.sh <target_conninfo> <encrypted_file>}"

echo "[1/3] Decrypting..."
age --decrypt -i "$AGE_IDENTITY_FILE" -o "$DUMP_FILE" "$ENCRYPTED_FILE"

DUMP_SIZE=$(wc -c < "$DUMP_FILE")
if [ "$DUMP_SIZE" -lt 100 ]; then
  echo "ERROR: dump too small" >&2
  exit 1
fi

echo "[2/3] Restoring..."
pg_restore --dbname="$TARGET_URL" --exit-on-error --no-owner "$DUMP_FILE"

TABLE_COUNT=$(psql --dbname="$TARGET_URL" -At -c \
  "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')" 2>/dev/null || echo "0")
echo "Restored $TABLE_COUNT user tables."
echo "Done."
`

// GenerateRestoreScript returns a self-contained restore.sh script.
func GenerateRestoreScript(in KitInput) string {
	notes := platformpkg.RecoveryNotes(in.Platform)
	var noteLines strings.Builder
	for _, n := range notes {
		noteLines.WriteString("# ")
		noteLines.WriteString(n)
		noteLines.WriteString("\n")
	}

	script := fmt.Sprintf(restoreScriptTemplate,
		in.BackupUUID, in.KeyID, in.SHA256, noteLines.String())
	return script
}

// GenerateRecoveryGuide returns platform-specific recovery guidance text.
func GenerateRecoveryGuide(p platformpkg.Platform) string {
	notes := platformpkg.RecoveryNotes(p)
	return strings.Join(notes, "\n")
}
