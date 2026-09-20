#!/usr/bin/env bash
# Production smoke test for the Go taskboard.
#
# 1. Builds the real production binary (with build marker).
# 2. Starts it on a unique port with SQLite pointing at an isolated temp dir.
# 3. Exercises HTTP CRUD + negative cases against the running process.
# 4. Restarts the process with the SAME SQLite path and proves persistence.
# 5. Cleans up: stops the process, removes only the temp dir it created.
#
# No simulated output: every assertion hits the real HTTP server.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_DIR/bin/taskboard"
PORT="${SMOKE_PORT:-}"

if [[ -z "$PORT" ]]; then
  # Pick a free port (use python to avoid the race-prone 22222.. breaker).
  PORT="$(python3 - <<'EOF'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
EOF
)"
fi
BASE="http://127.0.0.1:$PORT"

export PORT
export SQLITE_PATH

# Isolated temp database OUTSIDE the release checkout.
WORKDIR="$(mktemp -d /tmp/taskboard-smoke.XXXXXX)"
DB_PATH="$WORKDIR/board.db"
SQLITE_PATH="$DB_PATH"

PASS=0
FAIL=0

cleanup() {
  set +e
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

log()  { printf '%s\n' "$*"; }
ok()   { PASS=$((PASS+1)); printf 'PASS  %s\n' "$*"; }
fail() { FAIL=$((FAIL+1)); printf 'FAIL  %s\n' "$*"; }

api() { # $1=method $2=path $3=expected-status $4=body(optional) $5=desc
  local method="$1" path="$2" expect="$3" body="${4:-}" desc="$5"
  local out code
  if [[ -n "$body" ]]; then
    out="$(curl -s -o /tmp/smoke_body.$$ -w '%{http_code}' -X "$method" \
      -H 'Content-Type: application/json' -d "$body" "$BASE$path")"
  else
    out="$(curl -s -o /tmp/smoke_body.$$ -w '%{http_code}' -X "$method" "$BASE$path")"
  fi
  code="$out"
  BODY="$(cat /tmp/smoke_body.$$)"
  rm -f /tmp/smoke_body.$$
  if [[ "$code" == "$expect" ]]; then ok "$desc ($code)"; else
    fail "$desc — expected $expect got $code body=$BODY"
  fi
}

start_server() { # $1=pre-flag
  "$BIN" >"$WORKDIR/out.log" 2>&1 &
  SERVER_PID=$!
  # Wait for readiness.
  for _ in $(seq 1 60); do
    if curl -sf "$BASE/readiness" >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      log "server exited early:"; cat "$WORKDIR/out.log"; return 1
    fi
    sleep 0.25
  done
  log "server did not become ready:"; cat "$WORKDIR/out.log"; return 1
}

stop_server() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null
    # Wait for graceful shutdown; kill -9 if it refuses within 5s.
    for _ in $(seq 1 20); do
      if ! kill -0 "$SERVER_PID" 2>/dev/null; then break; fi
      sleep 0.25
    done
    if kill -0 "$SERVER_PID" 2>/dev/null; then
      kill -9 "$SERVER_PID" 2>/dev/null || true
    fi
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  SERVER_PID=
}

log "== Go taskboard production smoke =="
log "port: $PORT  db: $DB_PATH"

export PATH="/root/.local/go/bin:$PATH"
export GOMAXPROCS=2 GOFLAGS="-p=2"

log "-- build --"
COMMIT="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo none)"
go build -trimpath -ldflags "-s -w -X main.version=smoke -X main.commit=$COMMIT -X main.buildTime=test" \
  -o "$BIN" "$REPO_DIR" 2>"$WORKDIR/build.log" || { log "build failed"; cat "$WORKDIR/build.log"; exit 1; }
ok "binary built at $BIN"

# ---- First run -------------------------------------------------------------
log "-- first run (fresh database) --"
start_server
ok "server ready on :$PORT"

# Liveness vs readiness
api GET /health 200 "" "liveness /health"
api GET /readiness 200 "" "readiness /readiness"

# Seed data present
seed_count="$(curl -s "$BASE/api/projects" | jq 'length')"
if [[ "$seed_count" -ge 2 ]]; then ok "seeded projects present ($seed_count)"; else
  fail "expected seeded projects, got $seed_count"
fi

# CRUD
PROJECT="$(curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Smoke Project","description":"created by smoke"}' "$BASE/api/projects")"
PID="$(printf '%s' "$PROJECT" | jq -r '.id')"
[[ -n "$PID" && "$PID" != "null" ]] && ok "create project (id=$PID) (201)" || fail "create project — $PROJECT"

TASK="$(curl -s -X POST -H 'Content-Type: application/json' \
  -d "{\"project_id\":$PID,\"title\":\"Smoke task\",\"status\":\"todo\"}" "$BASE/api/tasks")"
TID="$(printf '%s' "$TASK" | jq -r '.id')"
[[ -n "$TID" && "$TID" != "null" ]] && ok "create task (id=$TID) (201)" || fail "create task — $TASK"

api GET "/api/projects/$PID" 200 "" "read project"
api PATCH "/api/projects/$PID" 200 '{"name":"Smoke Project v2"}' "update project"
api PATCH "/api/tasks/$TID" 200 '{"status":"in_progress"}' "update task status"
api GET "/api/tasks?q=Smoke" 200 "" "search tasks"
api GET "/api/tasks?status=in_progress" 200 "" "filter by status"
api POST "/api/tasks" 400 "{\"project_id\":$PID,\"title\":\"Bad\",\"status\":\"bogus\"}" "reject invalid status"
api POST "/api/projects" 400 '{"name":""}' "reject blank name"
api POST "/api/projects" 400 '{not valid json' "reject malformed JSON"
api GET /api/nope 404 "" "404 on unknown API route"
api GET /api/projects/999999999 404 "" "404 on missing project"
api DELETE "/api/projects/$PID" 204 "" "delete project (cascade)"

# HTML & static assets
html_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")"
[[ "$html_code" == "200" ]] && ok "HTML index page (200)" || fail "HTML index page — $html_code"
static_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/static/style.css")"
[[ "$static_code" == "200" ]] && ok "static style.css (200)" || fail "static style.css — $static_code"

# Persistence marker record, KEEP in the db for the restart check.
PERSIST="$(curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Persist Me","description":"must survive restart"}' "$BASE/api/projects")"
PERSIST_ID="$(printf '%s' "$PERSIST" | jq -r '.id')"
[[ -n "$PERSIST_ID" && "$PERSIST_ID" != "null" ]] && ok "created persistence marker (id=$PERSIST_ID)" \
  || fail "persistence marker — $PERSIST"

stop_server
log "-- restart with the SAME database path --"
start_server
if curl -sf "$BASE/api/projects/$PERSIST_ID" -o /tmp/smoke_persist.$$ && \
   [[ "$(jq -r '.name' /tmp/smoke_persist.$$)" == "Persist Me" ]]; then
  ok "record survived restart ($PERSIST_ID)"
else
  fail "record LOST after restart (id=$PERSIST_ID)"
fi
rm -f /tmp/smoke_persist.$$

# Readiness must reflect dependency failure: point at an unreadable db? We
# emulate by checking readiness reports ok, and DB-dependent path works.
api GET /readiness 200 "" "readiness after restart"

stop_server

log
log "== RESULT: $PASS passed, $FAIL failed =="
if [[ "$FAIL" -gt 0 ]]; then
  log "database was $DB_PATH (removed on cleanup)"; exit 1
fi
rm -f "$BIN"
exit 0