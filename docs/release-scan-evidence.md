# Pre-release scan evidence

Point-in-time local runs of the same gates CI enforces, recorded against the
commit they scanned. Re-run after every release-candidate commit; these
results say nothing about later states of the tree.

## Scan of a645869811d149637d7bd1c33f3184c4a4f41264 (2026-10-08)

| Gate | Tool / version | Command (abridged) | Result |
|---|---|---|---|
| Secret scan, full history | gitleaks 8.30.1 | `gitleaks detect --source . --log-opts=--all --redact` | **PASS** — 98 commits, ~3.82 MB, `no leaks found` |
| Go vulnerability audit | govulncheck v1.8.0 (Go 1.26.6, vuln DB 2026-10-07) | `govulncheck ./...` | **PASS** — 0 vulnerabilities affecting called code (1 in a required module, not called) |
| Image CVE gate (CI-equivalent) | trivy 0.66.0 | `trivy image --scanners vuln --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1 supabackup:runtime` | **PASS** — OS layer (debian 12.15) 0 findings, Go binary 0 findings |
| Release image builds | docker buildx | `docker build --target runtime` / `-f Dockerfile.spike` / default (no `--target`) | **PASS** — all three rc=0; default build equals the runtime stage |

### Note on the image scan

A first trivy run **without** `--ignore-unfixed` reported fixable
CRITICAL/HIGH CVEs in `perl*` and `libpcre2-8-0` (e.g. CVE-2026-13221,
CVE-2026-8376, CVE-2026-103111). Root cause: a stale local
`debian:bookworm-slim` cache predating Debian 12.15 point releases. After
`docker pull debian:bookworm-slim` and a rebuild, the CI-equivalent gate
(`--ignore-unfixed`) passes with 0 findings in both layers.

Operational takeaway: the CI trivy gate inherits freshness from the runner's
base-image pull, but local release builds must `docker pull` the base image
first — a cached base can silently ship OS packages with known fixes
available. The runtime image itself adds no vulnerable layer of its own.
