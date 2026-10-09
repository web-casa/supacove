# Pre-release scan evidence

> 本文档记录改名（supabackup → supacove）之前的一次发布前扫描；为保持文档与现状一致，正文中的产品名已更新为 supacove，涉及当时实际产物名的位置以括注说明。

Point-in-time local runs of the same gates CI enforces, recorded against the
commit they scanned. Re-run after every release-candidate commit; these
results say nothing about later states of the tree.

## Scan of a645869811d149637d7bd1c33f3184c4a4f41264 (2026-10-08)

| Gate | Tool / version | Command (abridged) | Result |
|---|---|---|---|
| Secret scan, full history | gitleaks 8.30.1 | `gitleaks detect --source . --log-opts=--all --redact` | **PASS** — 98 commits, ~3.82 MB, `no leaks found` |
| Go vulnerability audit | govulncheck v1.8.0 (Go 1.26.6, vuln DB 2026-10-07) | `govulncheck ./...` | **PASS** — 0 vulnerabilities affecting called code (1 in a required module, not called) |
| Image CVE gate (CI-equivalent) | trivy 0.66.0 | `trivy image --scanners vuln --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1 supacove:runtime`（当时镜像名为 supabackup:runtime） | **PASS** — OS layer (debian 12.15) 0 findings, Go binary 0 findings |
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

## Environment-blocked acceptances closed on real infrastructure (2026-10-08, HEAD ca8973d)

glm-round-review-2 concluded NEEDS_FIXES *solely* because the reviewer
sandbox could not listen on sockets or run docker; the three outstanding
acceptances were re-run on this machine (docker available, real sockets)
against the same code:

| Acceptance | How it was run | Result |
|---|---|---|
| Real promtool on live `/metrics` | `supacove serve`（当时二进制名为 supabackup）on 127.0.0.1:18099, bootstrap 201 + login 200 via cookie jar, scraped `/metrics` (41 lines, 14 families, 当时前缀 `supabackup_*`) piped to `docker run --entrypoint promtool prom/prometheus check metrics` | **PASS — promtool rc=0** |
| MinIO storage tests ×2, no residue | `go test -count=1 ./backend/internal/storage/ -run 'MinIO|AbortIncomplete'` twice (4.2s / 3.5s) | **PASS both runs; `docker ps -a --filter name=sb-minio` → 0 leftovers** |
| govulncheck v1.8.0 networked scan | already recorded above for a645869; unchanged code paths | **PASS — 0 called vulnerabilities** |

Operational note captured during this run: two stale `supacove serve`（当时名为 supabackup）
processes from an earlier acceptance attempt kept 127.0.0.1:18099 bound, so
a fresh server silently failed to bind and curl hit the stale instance
(deleted data dir → bogus 403/401). Killed by exact PID (never `pkill -f`,
which matches the invoking shell) and re-run clean. Always verify the port
is released before treating an auth failure as a product defect.
