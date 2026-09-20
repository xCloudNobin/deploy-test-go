# Taskboard (deploy-test-go)

A compact project/task board built with Go's standard library and a real
SQLite database (`modernc.org/sqlite`, pure-Go — no CGO, no external DB
service). It replaces the original "go deployment test" shell with a
meaningful app usable as a live deployment probe.

This is a **public demo**: there is **no authentication** and anyone who can
reach the port can read, create, edit and delete everything. Do not put
sensitive data in it.

## Features

- Projects group tasks; project and task CRUD.
- Status workflow: `todo` / `in_progress` / `done`, with quick status select
  in the UI (progressive enhancement; works without JS).
- Search across task title/description and a status filter on the board.
- JSON API (`/api/projects`, `/api/tasks`) for tooling and the smoke test.
- Deterministic schema initialization (idempotent `IF NOT EXISTS`) and seed
  data inserted only on an empty database.
- **Persistent SQLite** storage: data survives restart *and* redeploy when
  `SQLITE_PATH` points outside the release checkout.
- Graceful shutdown on SIGTERM/SIGINT, health and dependency-aware readiness.

## Requirements

- Go `1.25+` (built and verified with Go `1.27.1`).
- No CGO, no external database.

## Build

```sh
make build        # -> bin/taskboard (GOMAXPROCS=2, GOFLAGS="-p=2", ldflags marker)
make test         # go test ./... with the same caps
```

The compile-time ldflags embed a release/build marker (`version`, `commit`,
`buildTime`) that the UI renders in the top bar and `/health` reports.

## Run (production)

```sh
export PORT=8080                       # configurable port, defaults to 8080
export SQLITE_PATH=/var/lib/taskboard/board.db   # persistent path OUTSIDE the release checkout
make build
./bin/taskboard
```

The server binds to `0.0.0.0` and logs to stdout/stderr. On shutdown it
drains in-flight requests (10s timeout) before exiting.

- Liveness: `GET /health` → `200 {"status":"ok","app":...,"version":...}`.
- Readiness: `GET /readiness` → `200` only when the DB answers a ping,
  otherwise `503`.

## Environment variables

| Variable      | Required | Default                  | Notes                                        |
|---------------|----------|--------------------------|----------------------------------------------|
| `PORT`        | no       | `8080`                   | TCP port; bind host is always `0.0.0.0`.     |
| `SQLITE_PATH` | no       | `./data/board.db`        | **Use an absolute path outside the checkout** (e.g. `/var/lib/taskboard/board.db`) so redeploys keep data. `:memory:` is for throwaway tests only. |
| `DB_PATH`     | no       | (alias for `SQLITE_PATH`)| Accepted if `SQLITE_PATH` is unset.          |

See `.env.example` for safe example values. No secrets are required
(no authentication in this demo).

## Persistence and schema

- Schema is applied on every start and is idempotent (`CREATE TABLE IF NOT
  EXISTS ...`, `CREATE INDEX IF NOT EXISTS ...`). Repeated application is a
  no-op.
- Seed inserts two sample projects (with tasks) only when the projects table
  is empty, so it is deterministic and never duplicates data.
- `tasks.project_id` references `projects.id` with `ON DELETE CASCADE`.
- All queries are parameterized; search uses `LIKE` with `ESCAPE` and
  wildcard characters in user input are escaped.
- HTML is rendered with `html/template` (auto-escaping); JSON accepts a
  bounded (1 MiB), strictly-schemed body.
- The database directory is created if missing.

## Tests and smoke verification

```sh
make test                # unit/integration suite (real SQLite:memory:)
./scripts/smoke.sh       # production process smoke: build → HTTP CRUD + negatives
                         #   → restart with same SQLite path → persistence proven → cleanup
```

`scripts/smoke.sh` starts the actual built binary on a unique ephemeral port
with SQLite in an isolated temp dir, performs create/read/update/delete,
search, filter, invalid-input and not-found checks, restarts the process
against the *same* database file, verifies a marker record survived, then
cleans up the temp dir and stops the process. It requires `go`, `curl`
and `jq`.

## Project layout

```
app.go        routes + embedded template catalog (per-page template sets)
db.go         SQLite open, schema, deterministic seed
handlers.go   pages, JSON API, health/readiness, input handling
store.go      parameterized queries (projects/tasks)
validate.go   validation rules + allowed statuses
main.go       config, graceful shutdown, build marker
templates/    shared layout + per-page HTML
static/       CSS + small JS (progressive enhancement)
scripts/      smoke.sh
```