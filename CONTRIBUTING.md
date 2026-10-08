# Contributing

Thanks for your interest — supabackup is a safety-critical tool (backups), so
the bar for changes is "provably does not fake success".

## Ground rules

- **Never make a failure path look like success.** Any change to the commit
  chain (dumper/staging/remote commit/verification) needs a fault-injection
  test: the injected failure must turn the suite red (mutation-check your
  test by deleting the protection once).
- New user-visible strings ship in **both** locales
  (`frontend/src/i18n/en.ts` + `zh-CN.ts`, backend `i18n.Msg` pairs); the
  `Dict` type and parity tests enforce symmetry.
- API changes go through `api/openapi.yaml`; run `make api-gen` and commit
  generated code. Breaking changes require a release-tag baseline update
  (the `contract` CI job rejects baseline moves in PRs).
- Comments state constraints the code cannot show; do not narrate the diff.

## Local gates (what CI runs)

```bash
make check        # gofmt/vet/golangci-lint + eslint/stylelint + vitest + contract + backend tests
make api-check    # generated code drift
make e2e          # Playwright against the embedded binary (needs docker for nothing; host tools only)
go test -race ./backend/...
```

Docker-dependent tests (real PostgreSQL/MinIO) skip gracefully without it,
but a PR touching those paths should run them locally and say so.

## Pull request checklist

- [ ] tests added/updated for every behavior change (fault matrix where relevant)
- [ ] `docs-site` content updated when user-visible behavior/config changed (both locales)
- [ ] CHANGELOG entry under `[Unreleased]`
- [ ] no secrets, session plans or machine-local paths in the diff
