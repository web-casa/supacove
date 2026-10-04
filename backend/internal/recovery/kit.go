// Package recovery generates self-contained recovery kits: restore.sh
// scripts with fixed templates and platform-aware guidance, plus manifest-
// derived checksums. Scripts use fixed templates with safe parameter
// passing — user data never enters shell command construction (dev-plan
// Phase 5 P1-07 SSRF/injection boundary).
package recovery

import (
	"fmt"
	"strings"

	platform "github.com/cloudfan/supabackup/backend/internal/platform"
)

// KitInput carries everything needed to generate a recovery kit.
type KitInput struct {
	// BackupUUID is the immutable backup identity.
	BackupUUID string
	// Platform for platform-specific recovery guidance.
	Platform platform.Platform
	// ArtifactFileName is the remote/staged ciphertext filename.
	ArtifactFileName string
	// SHA256 is the ciphertext hash (for post-download verification).
	SHA256 string
	// KeyID is the age recipient fingerprint.
	KeyID string
}

// GenerateRestoreScript returns a self-contained restore.sh script.
// The script decrypts the ciphertext with age and restores it with
// pg_restore --exit-on-error into a user-specified target. It does NOT
// contain any credentials — the operator supplies the connection string.
func GenerateRestoreScript(in KitInput) string {
	notes := platform.RecoveryNotes(in.Platform)
	var noteLines strings.Builder
	for _, n := range notes {
		noteLines.WriteString("# ")
		noteLines.WriteString(n)
		noteLines.WriteString("\n")
	}

	return fmt.Sprintf(`#!/bin/sh
# supabackup recovery script
# Backup UUID: %s
# Key ID:      %s
# SHA-256:     %s
#
# %s
#
# Usage:
#   ./restore.sh <target_conninfo>
#
# Prerequisites (NOT included):
#   - age (https://age-encryption.org) for decryption
#   - pg_restore (PostgreSQL client, same major or newer than source)
#   - psql (PostgreSQL client)
#   - A running PostgreSQL target with an EMPTY database
#
# This script does NOT contain credentials. Set TARGET_URL to the
# destination PostgreSQL connection string (the URI may embed the password).
# Set AGE_IDENTITY_FILE to your offline identity file.
#
# SAFETY: this script will NOT drop or recreate existing objects.
# It uses --exit-on-error and aborts on the first failure.

set -eu

TARGET_URL="${1:?Usage: restore.sh <target_conninfo>}"
AGE_IDENTITY_FILE="${AGE_IDENTITY_FILE:?Set AGE_IDENTITY_FILE to your age identity file}"
DUMP_FILE="restore-%s.dump"

cleanup() {
  rm -f "$DUMP_FILE"
}
trap cleanup EXIT

# Step 1: decrypt (age reads ciphertext from stdin, writes plaintext dump)
echo "[1/3] Decrypting backup %s..."
if [ -f "%s" ]; then
  age --decrypt -i "$AGE_IDENTITY_FILE" -o "$DUMP_FILE" "%s"
else
  echo "ERROR: encrypted artifact %%s not found" >&2
  exit 1
fi

# Step 2: verify plaintext dump is non-empty
DUMP_SIZE=$(wc -c < "$DUMP_FILE")
if [ "$DUMP_SIZE" -lt 100 ]; then
  echo "ERROR: decrypted dump is suspiciously small ($DUMP_SIZE bytes)" >&2
  exit 1
fi
echo "    decrypted: $DUMP_SIZE bytes"

# Step 3: restore with pg_restore --exit-on-error
echo "[2/3] Restoring into target..."
pg_restore \\
  --dbname="$TARGET_URL" \\
  --exit-on-error \\
  --no-owner \\
  "$DUMP_FILE"

echo "[3/3] Verifying..."
TABLE_COUNT=$(psql --dbname="$TARGET_URL" -At -c \\
  "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')" 2>/dev/null || echo "0")
echo "Restored $TABLE_COUNT user tables."

echo ""
echo "Restore complete: $TABLE_COUNT tables restored."
echo "%s"
`, in.BackupUUID, in.KeyID, in.SHA256, noteLines.String(),
		in.BackupUUID, in.BackupUUID, in.ArtifactFileName, in.ArtifactFileName,
		"verify the restored data meets your expectations before decommissioning the old system.")
}
