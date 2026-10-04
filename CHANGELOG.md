# Changelog

All notable changes to supabackup.

## [0.1.0-alpha] — Phase 1–8

### Backup kernel
- PostgreSQL logical backup via `pg_dump --format=custom` with streaming age encryption
- Immutable backup UUID per job (SQLite rebuild cannot overwrite existing backups)
- Task state machine: pending → running → succeeded / failed / canceled / interrupted
- Seven error classes with retryable flag
- Staging area with quota enforcement, orphan cleanup, and durable writes

### Object storage (BYOS)
- S3-compatible destinations: AWS S3, Cloudflare R2, Backblaze B2
- Protocol C: intent → ciphertext PUT → read-back SHA-256 verification → manifest publish → remote commit
- Multipart upload with automatic abort on failure
- Protocol D: retention with anchor protection (newest committed backup untouchable)
- Bucket reconciliation (orphaned / missing / uncommitted classification)
- Presigned GET URLs for remote backup download (15 min expiry)

### Scheduling
- Per-database cron expressions with IANA timezone
- Freshness tracking by export snapshot time
- Webhook notifications for failure/expired events
- Atomic cursor advance + enqueue in a single transaction

### Security
- Single-admin bootstrap via one-time CLI token (no public takeover)
- Argon2id password hashing
- Server-side sessions with HMAC-bound CSRF tokens, `__Host-` prefix in production
- Login rate limiting with per-IP fixed window
- AES-256-GCM for credentials at rest (unique nonce, explicit auth failure)
- age encryption for backup files (identity shown once, stored offline)
- Credentials never in argv, PGPASSFILE with 0600 perms, env PG* stripped
- Escape-aware secret redaction across all error paths

### Verification
- Embedded restore verification: throwaway PG instance (initdb → pg_restore → check → stop), no Docker socket
- Same-UID deployment constraint (NOT a sandbox — ADR-004)

### Observability
- Prometheus `/metrics` (jobs by status, last success age, databases, destinations)
- Structured JSON logging
- Dead-man's switch heartbeat after successful backup
- Health endpoints (`/api/healthz` liveness, `/api/ready` readiness)

### Known limitations
- Go 1.26.0 stdlib vulnerabilities (19 findings — fixed by toolchain updates, not code changes)
- Restore verification does not check row-level data content (structure + count only)
- Single admin user (no team/RBAC)
- No PITR / WAL archiving (logical backup only)
- Destination version IDs not fully used for B2 versioned buckets
