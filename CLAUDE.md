# CLAUDE.md

You are a software engineer building `claw` and `molt` and other claw agent operators. You always write sufficient tests to thoroughly exercise all code paths before, update documentation in all places, and lint code before commiting.

## What this is

`claw` is a universal CLI orchestrator for claw agent architectures. It delegates to architecture-specific driver binaries (`claw-driver-nanoclaw`, `claw-driver-zepto`, etc.) via NDJSON on stdin/stdout — the same driver pattern used by `molt` (`/Users/ken/src/molt`).

## Build and test

```bash
# Build claw binary → ./build/claw
make build

# Build all drivers → ./build/claw-driver-*
make build-drivers

# Build and install everything → ~/.local/bin/
make install-all

# Run all tests (claw + all drivers)
make test

# Run tests for claw only
go test ./...

# Run tests for the nanoclaw driver only
cd drivers/nanoclaw && go test ./...

# Run a single test
cd drivers/nanoclaw && go test -run TestName ./...

# Lint (requires golangci-lint)
make lint

# Format all Go source files
make fmt
```

## Architecture

**Three separate Go modules** (same layout as molt):
- Root module (`go.mod`) — the `claw` CLI binary, in `src/`. Uses Cobra.
- `drivers/nanoclaw/go.mod` — the nanoclaw driver, a standalone binary.
- `drivers/zepto/go.mod` — the zepto driver, a standalone binary.

Drivers are independently built and discovered at runtime via `~/.claw/drivers/` then `$PATH`, matching `claw-driver-*`. Adding a new driver means creating a `drivers/<arch>/` directory with its own `go.mod`.

**Data flow (CLI):**
```
claw <command> → driver.go locates claw-driver-<arch>
              → spawns driver, writes NDJSON request to stdin
              → reads NDJSON response stream from stdout
              → formats output for terminal
```

**Data flow (API server):**
```
claw api serve → starts HTTP+WebSocket server on localhost:7474
  GET /api/v1/ps      → fans out ps_request to all drivers concurrently → merges JSON response
  GET /api/v1/sessions → sessions_request → merged day-based + Claude JSONL sessions
  WS /ws/watch/main   → spawns driver with watch_request → streams NDJSON as WS messages
  WS /ws/agent/main   → reads prompt from WS → spawns driver → streams response → loops for multi-turn
  WS /ws/logs/main    → spawns driver with logs_request → streams container stdout/stderr
```

**Key packages:**
- `driver/` — driver discovery (`FindAll`, `Locate`), version probe, NDJSON protocol methods
- `api/` — HTTP+WebSocket API server (`claw api serve`), translates driver NDJSON to HTTP/WS for `claw-console`
- `console/` — embedded claw-console static assets (Go embed); `--console` flag serves dashboard on same port
- `src/cmd/` — Cobra commands (`repl`, `agent`, `ps`, `watch`, `health`, `archs`, `api`, `molt`, `completion`)
- `drivers/nanoclaw/` — standalone NanoClaw driver binary (separate Go module)
- `drivers/zepto/` — standalone ZeptoClaw driver binary (separate Go module)

## Driver protocol

Drivers communicate via newline-delimited JSON on stdin/stdout. Request types:
- `version_request` / `version_response` — driver identity and compatibility
- `probe_request` / `probe_response` — auto-detect architecture from a path
- `ps_request` → streams `instance` messages, then `ps_complete`
- `agent_request` → streams `agent_output` chunks, then `agent_complete`
- `watch_request` → streams `message` rows continuously until stdin closes
- `health_request` → streams `check_result` messages, then `health_complete`
- `groups_request` → streams `group` messages, then `groups_complete`
- `sessions_request` → streams `session` messages (with optional `resumable` flag), then `sessions_complete`
- `logs_request` → streams `log_line` messages (container stdout/stderr) until stdin closes

See `spec/DRIVER.md` for the full protocol spec.

## NanoClaw driver internals

- `db.go` — SQLite helpers: open `store/messages.db`, read groups, read messages, fuzzy group matching
- `ps.go` — queries container runtime (Docker or Apple Containers) + joins with SQLite `registered_groups`; longest-prefix match handles `nanoclaw-<folder>-<timestamp>` style container names
- `agent.go` — resolves group, reads secrets from `.env`, spawns `nanoclaw-agent` container with structured mounts (`/workspace/group`, `/workspace/project`, `/home/node/.claude`), streams output through NDJSON. `--native` bypasses the container and runs the agent-runner via Node.js directly; `--verbose` pipes agent-runner stderr to the terminal. Supports `--timeout` (kills node process on deadline), `--ephemeral` (disposable workspace with no session persistence), and `--template` (prompt templating with `{input}` placeholder).
- `watch.go` — emits historical messages then polls SQLite for new rows; exits on stdin close
- `health.go` — runs 8 health checks (runtime, credentials, database, disk, sessions, groups, skills, image) using existing helpers; streams `check_result` messages. Image check shows container runtime name (Apple Containers / Docker) + image name + build date.
- `groups.go` — lists registered groups from SQLite via `readGroupRows()`; streams `group` messages
- `sessions.go` — derives sessions from messages table grouped by day, then merges Claude session UUIDs from JSONL files (`data/sessions/<folder>/.claude/projects/*/*.jsonl`). Matched sessions get the UUID as `session_id` and `resumable: true`. One entry per day — no duplicates.
- `logs.go` — streams container stdout/stderr for a group. Resolves group to the most recently started container (Apple Containers matches by `/workspace/group` mount path; Docker matches by `nanoclaw-<folder>` name prefix). Tails last 100 lines then follows.

Source-dir detection: `NANOCLAW_DIR` env var → walk up from binary → `~/src/nanoclaw`.

## ZeptoClaw driver internals

- `probe.go` — detects ZeptoClaw via `~/.zeptoclaw/config.json` and binary presence; overridable via `ZEPTOCLAW_DIR` / `ZEPTOCLAW_BIN`
- `agent.go` — sends requests to `zeptoclaw agent-stdin` using the gateway IPC format; threads session keys for REPL continuity
- `ps.go` — checks both native `zeptoclaw gateway/daemon` processes and `zeptoclaw-*` containers
- `watch.go` — polls `~/.zeptoclaw/sessions/` for CLI session messages (500ms interval)
- `health.go` — runs applicable health checks (runtime, credentials, disk, sessions); database/groups/image return "not applicable"
- `groups.go` — returns UNSUPPORTED (zepto has no groups database)
- `sessions.go` — reads `~/.zeptoclaw/sessions/*.json` files; streams `session` messages

## Relationship to other projects

- **molt** (`/Users/ken/src/molt`) — sibling Go CLI for migration between architectures. Same driver discovery pattern, same NDJSON protocol style, same project layout. Copy patterns from here.
- **nanoclaw** (`/Users/ken/src/nanoclaw`) — TypeScript agent service. The `scripts/nanoclaw` Python CLI is the source of truth for container spawning, ps, watch, and history logic that `claw-driver-nanoclaw` ports to Go.
- Container sentinels: `---NANOCLAW_OUTPUT_START---` / `---NANOCLAW_OUTPUT_END---` delimit the JSON result in container stdout.
