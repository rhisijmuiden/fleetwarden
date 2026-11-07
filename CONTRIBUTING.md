# Contributing to FleetWarden

## Branches and releases

| Branch | Channel | What happens on merge |
| --- | --- | --- |
| `test` | Test | CI publishes a pre-release `vX.Y.Z-test.N` |
| `main` | Production | CI publishes the release `vX.Y.Z` from the `VERSION` file |

1. Work on a feature branch and open a pull request against `test`.
2. CI must pass: Go vet/tests, web i18n check and build, shellcheck, Debian package
   build and an install test on a clean Debian 13 container. Pull requests
   into `test` are merged automatically when CI is green.
3. A phase is promoted with a release pull request from `test` to `main`,
   which a maintainer merges by hand. Bump `VERSION` before promoting.

## Local development

```sh
make test                        # Go vet + tests, i18n check, typecheck
# API integration tests need an empty PostgreSQL database (CI provides one):
export FW_TEST_DATABASE_URL=postgres://fwtest:fwtest@127.0.0.1:5432/fwtest?sslmode=disable
FW_DATABASE_URL=... FW_DATA_DIR=./tmp go run ./cmd/fleetwarden-server  # API on 127.0.0.1:8080
cd web && npm ci && npm run dev  # UI with hot reload, proxies /api to the server
make all deb                     # binaries and the .deb in dist/ (needs nfpm)
```

## Rules

- Every user-facing string goes through i18n with both `en.json` and `nl.json`
  (English is the default). `npm run test:i18n` enforces matching keys.
- Colours come from `branding/` tokens, never hard-coded, so white-label works.
- Database changes are new files in `internal/db/migrations/` (`NNNN_name.sql`),
  applied in order at start-up. Migrations are forward-only.
- Every change by a user goes into the audit log.
- Contributions require agreeing to the [CLA](CLA.md).
