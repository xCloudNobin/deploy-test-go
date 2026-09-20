# Verification — deploy-test-go (Go taskboard)

Date: 2026-09-20
Repository: `xCloudNobin/deploy-test-go`
Branch: `feat/compatibility-taskboard`

## Environment

- Go toolchain: `go1.27.1 linux/amd64` (local toolchain, no system packages
  installed, no global runtime mutation).
- Build caps: `GOMAXPROCS=2`, `GOFLAGS=-p=2`.
- Runtime: built binary (`bin/taskboard`), no Docker, no dev server.
- Storage: SQLite via `modernc.org/sqlite` (pure Go, no CGO).

## Commands and results

### 1. Clean build

```
$ export PATH=/root/.local/go/bin:$PATH  GOMAXPROCS=2 GOFLAGS=-p=2
$ make build
GOMAXPROCS=2 GOFLAGS="-p=2" go build -trimpath -ldflags "-s -w -X main.version="1.0.0" ...
exit 0   → bin/taskboard (about 13 MB)
```

### 2. Unit/integration tests (real SQLite:memory:)

```
$ make test
ok   github.com/xCloudNobin/deploy-test-go   0.106s
```

With race detector:

```
$ go test -race ./...
ok   github.com/xCloudNobin/deploy-test-go   1.629s
```

18 tests, all passing, including:

- Health payload + build marker (`/health` stays the `status/app/version`
  JSON contract of the original app).
- Readiness reports `200` when the DB pings and `503` after the database is
  closed (dependency-aware; not a static marker).
- Project CRUD + task CRUD, cascade delete of tasks when a project is removed.
- Search with escaped LIKE wildcards (`%` is literal, no pattern injection).
- Status filter validation (invalid status ignored server-side).
- Malformed JSON, oversized body, unknown JSON fields, blank/over-long names,
  invalid task status → all `400` with a meaningful error.
- HTML auto-escaping of user-supplied `<script>` content.
- Task form renders in both new and edit variants without template errors,
  with the selected project and current status marked.
- Schema idempotency + deterministic, non-duplicating seed.
- Persistence across full DB close/reopen (same on-disk file).
- Concurrent writes do not corrupt data.
- Config defaults/overrides (`PORT`, `HOST`, `SQLITE_PATH`, `DB_PATH`).

### 3. Production smoke (real process, real HTTP)

Script: `scripts/smoke.sh` — unique ephemeral port, isolated temp SQLite DB
outside the release checkout, cleanup on exit.

Result: **23 passed, 0 failed** (exit 0)

Covered over HTTP against the running production binary:

- liveness `/health` 200, readiness `/readiness` 200
- seeded projects present
- create/read/update project, create task, update task status
- search and status filter
- invalid status, blank name, malformed JSON → 400
- unknown API route and missing project → 404
- delete project (cascade)
- HTML index page + static asset 200
- **restart with the same SQLite path → marker record survived** (persistence
  proven across process restart)
- graceful shutdown (SIGTERM drain) observed in server logs
  (`received shutdown signal, draining connections` → `shutdown complete`)

Example restart line:

```
record survived restart (4)
```

## Live deployment

**NOT RUN.** No live xCloud deployment was performed; `deployment_verified` is
therefore **false**. All results above are local production-process
verification only. Live qualification on the intended xCloud category is
required before marking `deployment-verified`.

## Limitations

- Public demo, **no authentication/authorization**: anyone with network access
  to the port can mutate all data.
- SQLite database must be pointed at a persistent path outside the release
  checkout (`SQLITE_PATH`); default fallback is a local `./data/board.db`.
- Readiness depends on the SQLite connection being pingeable; it does not
  guard a lock held by a stale writer (single-connection pool serializes
  access).
- No CSRF protection: this app has no cookie-based authentication, so there
  are no authenticated mutations to protect. If credentials/auth are added
  later, CSRF tokens must be added for cookie-authenticated mutations and
  mark `deployment-verified` status accordingly.