# Changelog

All notable changes to supabackup.

## [Unreleased]

### Added
- Quality gates (adopted lightweight-toolchain plan): golangci-lint v2 curated
  config (lint-clean tree), stylelint, Vitest+RTL component layer, Playwright
  e2e against the embedded binary (scripts/e2e-run.sh), oasdiff breaking gate
  vs api/openapi-baseline.yaml, in-repo /metrics exposition-format gate plus
  `make metrics-check` for promtool, CI: actionlint, Trivy image scan, pinned
  tool versions, permissions: read, concurrency groups, image needs security,
  race under CGO=1 plus a CGO=0 production-conditions pass
- Fault-matrix tests: occupied-final-path commit, lost-artifact
  reconciliation, multipart-interrupted and lost-response uploads,
  concurrent enqueue admission, retention anchor across a new failure
- Standalone restore E2E: ciphertext + offline age identity + kit script
  restore without any application state; single-byte tamper rejected
- SB_JOB_TIMEOUT (6h default) and SB_FAILED_ARTIFACT_TTL_HOURS (72h default):
  wall-clock budget per backup job — a hung stage becomes a classified
  failure instead of silently stalling the single worker (overall review
  P1-K2) — and the reclamation grace window for dead artifacts

### Changed
- **BREAKING (metrics)**: gauge families dropped the illegal `_total` suffix
  (promtool lint rejects `_total` on gauges): `supabackup_jobs_total` →
  `supabackup_jobs`, `supabackup_verification_total` →
  `supabackup_verification`, `supabackup_remote_commits_total` →
  `supabackup_remote_commits`, `supabackup_remote_upload_failures_total` →
  `supabackup_remote_upload_failures`. The deprecated `_total` aliases were
  removed outright rather than kept, because an alias would still fail the
  promtool gate; pre-beta there are no shipped dashboards to migrate. Label
  values are now escaped with the Prometheus-legal set only (`\\`, `\"`,
  newline) — Go's `%q` previously emitted `\t`/`\xNN`, which real scrapers
  reject outright (GLM review round 5)
- `AbortIncomplete` (leftover multipart cleanup) now lists without any
  pagination marker and repeats to a fixed point: AWS treats a same-key
  KeyMarker as strictly-greater (skipping every session of that key) and
  MinIO invalidates both markers once earlier pages are aborted — marker
  pagination was wrong on one provider or the other either way (4/4
  observed MinIO runs left one of 101 sessions behind)

### Fixed
- Graceful shutdown (docker stop / upgrade) no longer lands a running backup
  as a false `failed` (with a false failure webhook and /fail heartbeat):
  shutdown-interrupted jobs are recorded as `interrupted` and their committed
  artifacts are re-uploaded by startup convergence (overall review P1-R1)
- Failed/canceled/interrupted jobs' staging artifacts are now reclaimed after
  a grace window (`SB_FAILED_ARTIFACT_TTL_HOURS`, default 72h) — previously
  they accumulated forever and could lock all backups once the staging quota
  was hit (overall review P1-K1)
- `totalDumpBytes` now measures the compressed pg_dump archive (counted
  before age encryption) instead of duplicating the ciphertext total
  (overall review P1-A1)
- Export success-rate denominator now counts jobs that began executing;
  queued cancels are excluded from both sides (contract documents the
  denominator) (overall review P1-A2)
- Migration 0008's Down section no longer breaks rollbacks below v8
- Startup recovery annotations are idempotent: repeated restarts no longer
  grow `error_message`
- `/metrics` requires a session; SSRF dial guard now also refuses the AWS
  IPv6 metadata endpoint and NAT64-encoded link-local addresses
- UI localization (en / zh-CN) across the console and API responses
  (Accept-Language), including remediation guidance

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

### Verification (Phase 5–8 hardening)
- Embedded restore verification: throwaway PG instance (initdb → pg_restore → expected-table check → confirmed stop), no Docker socket
- Same-UID deployment constraint (NOT a sandbox — ADR-004); enablement requires explicit config + age identity
- Ciphertext hash gate → age decrypt into a restricted 0600 temp file before restore
- Extension availability (`unsupported`) and expected-table-count classification; honest result wording (no overclaimed object-set proof)
- Bucket-only chain: pruned local artifacts are fetched from the committed remote object before verification
- Single verification worker, bounded queue bound to the Runner lifecycle; interrupted runs re-queue at startup
- Every exit path after a start attempt stops the throwaway instance; an unconfirmable stop preserves the workdir as evidence
- Unix-socket path overflow fallback for deeply nested data directories

### Recovery kits (Phase 5)
- Generated `restore.sh` per backup: ciphertext SHA-256 gate, empty-target refusal, signal-safe 0700 temp workspace, table-count cross-check
- Passwords only via `PGPASSWORD`; inline/percent-encoded/spaced keyword conninfo forms refused
- Kit persisted, downloadable (`GET /api/tasks/{id}/recovery-kit`), regenerated at startup
- Honest platform guidance: Supabase notes state the FULL-database scope (auth/storage schemas included; file blobs and platform services not)

### Notifications (Phase 7)
- Transactional notification outbox: state change + notification in one SQLite transaction; bounded exponential backoff (30s→1h, 5 attempts → dead), event-id dedup, crash convergence, 15-minute delivery leases
- Webhook CRUD + synchronous test delivery; deliveries carry `X-Supabackup-Event` / `X-Supabackup-Event-ID`; link-local (metadata) destinations refused at dial time
- Dead-man switch bound to real freshness: per-database url/period/grace; success ping gated on remote commit AND snapshot age; failures ping `/fail`; `-` disables per database
- Legacy Phase-4 subscription vocabulary converted by a per-token, re-entrant migration; Down restores old words so a rolled-back binary keeps matching

### Observability & UX (Phase 7–8)
- Protected `/metrics`: jobs by status, remote-commit totals, verification distribution, last success per database, staging bytes, outbox health, protection-state counts, `supabackup_scrape_errors`
- Dashboard: protection overview (fresh/expired/never + verification badges), backup-now, kit/artifact/presigned downloads, schedule+heartbeat forms, webhook management with test, delivery log
- Task API carries the seven error classes with operator remediation text
- Volume metrics: source DB physical size (`pg_database_size`) alongside dump-archive and ciphertext sizes

### Audits
- govulncheck: 0 vulnerabilities (Go 1.26.6+, stdlib advisories cleared)
- gitleaks: no leaks in the tree
- Secret canary regression test across the four exits (task errors, webhook payloads, artifacts, logs) — caught and fixed a password-value redaction gap

### Observability
- Prometheus `/metrics` (jobs by status, last success age, databases, destinations)
- Structured JSON logging
- Dead-man's switch heartbeat after successful backup
- Health endpoints (`/api/healthz` liveness, `/api/ready` readiness)

### Known limitations
- Toolchain updated to Go 1.26.6: govulncheck reports 0 known stdlib vulnerabilities affecting this build (CI gate: security job).
- Restore verification does not check row-level data content (structure + count only)
- Single admin user (no team/RBAC)
- No PITR / WAL archiving (logical backup only)
- Destination version IDs not fully used for B2 versioned buckets
