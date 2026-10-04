// Package manifest implements protocol E (dev-plan §0.5): every backup
// ships a self-describing manifest sufficient to restore WITHOUT this
// application — source/tool versions, selection rules, archive hashes and
// the age key ID. Manifests never contain passwords or private keys.
package manifest

import (
	"encoding/json"
	"time"
)

const FormatVersion = 1

type Manifest struct {
	FormatVersion int    `json:"formatVersion"`
	BackupID      string `json:"backupId"`
	// DumpStartedAt is when the pg_dump EXECUTION started — not the moment
	// the server snapshot was taken (that instant is not observable through
	// pg_dump and is honestly unknown; round-1 review P1-10).
	DumpStartedAt time.Time    `json:"dumpStartedAt"`
	FinishedAt    time.Time    `json:"finishedAt"` // artifact committed
	Database      Database     `json:"database"`
	Source        Source       `json:"source"`
	Backup        Backup       `json:"backup"`
	Selection     Selection    `json:"selection"`
	Verification  Verification `json:"verification"`
	Dependencies  Deps         `json:"dependencies"`
	Archive       Archive      `json:"archive"`
}

type Database struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	EnvTag   string `json:"envTag,omitempty"`
}

type Source struct {
	Host          string `json:"host"`
	Port          string `json:"port,omitempty"`
	DBName        string `json:"dbname"`
	ServerVersion string `json:"serverVersion"`
}

type ToolVersions struct {
	PGDump      string `json:"pgDump"`
	ClientMajor int    `json:"clientMajor"`
	Age         string `json:"age"`
}

type Backup struct {
	Mode         string       `json:"mode"`        // "full" (v1)
	Format       string       `json:"format"`      // "pg_dump custom (-Fc)"
	Compression  string       `json:"compression"` // zlib (custom-format default)
	ToolVersions ToolVersions `json:"toolVersions"`
	// RestoreNote states the verified/unsupported restore targets verbatim.
	RestoreNote string `json:"restoreNote"`
}

// Verification is honest about what has NOT been done (protocol E v1).
type Verification struct {
	RestoreVerified bool   `json:"restoreVerified"`
	Note            string `json:"note"`
}

type Selection struct {
	Schemas        []string `json:"schemas,omitempty"`
	ExcludeSchemas []string `json:"excludeSchemas,omitempty"`
	Rule           string   `json:"rule"` // human-readable selection statement
}

type Deps struct {
	Extensions       []PgExtension `json:"extensions"`
	Roles            []string      `json:"roles"`
	HasLargeObjects  bool          `json:"hasLargeObjects"`
	HasForeignTables bool          `json:"hasForeignTables"`
	ServerEncoding   string        `json:"serverEncoding,omitempty"`
	// TableCount is the user-table count at dump time — the expected-object
	// baseline for restore verification (phase-5 review P1-08). Serialized
	// WITHOUT omitempty deliberately: a genuine zero-table database must
	// stay distinguishable from a legacy manifest that lacks the field
	// (review round-2 R2-P1-02 — omitempty turned known-zero into unknown).
	// Consumers use HasTableCount to tell them apart.
	TableCount int64 `json:"tableCount"`
}

// PgExtension mirrors pgclient.Extension without importing it here.
type PgExtension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Encryption struct {
	Type      string `json:"type"`      // "age"
	KeyID     string `json:"keyId"`     // recipient fingerprint (protocol B)
	Recipient string `json:"recipient"` // public — safe to store
}

type Archive struct {
	FileName   string     `json:"fileName"`
	SHA256     string     `json:"sha256"` // over the full CIPHERTEXT (protocol C.1)
	SizeBytes  int64      `json:"sizeBytes"`
	Encryption Encryption `json:"encryption"`
}

// Writer produces the canonical JSON bytes for a manifest.
func Marshal(m *Manifest) ([]byte, error) {
	m.FormatVersion = FormatVersion
	return json.MarshalIndent(m, "", "  ")
}

// Unmarshal parses a manifest file's bytes.
func Unmarshal(b []byte) (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

// HasTableCount reports whether the raw manifest JSON carries the
// dependencies.tableCount field. Manifests written before the field existed
// decode TableCount as 0, which is indistinguishable from a genuinely empty
// database — callers must treat absence as unknown, never as "zero tables
// expected" (phase-5 review P1-08).
func HasTableCount(b []byte) bool {
	var raw struct {
		Dependencies *struct {
			TableCount *int64 `json:"tableCount"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return false
	}
	return raw.Dependencies != nil && raw.Dependencies.TableCount != nil
}
