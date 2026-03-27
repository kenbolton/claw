# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

**Data flow:**
```
claw <command> → driver.go locates claw-driver-<arch>
              → spawns driver, writes NDJSON request to stdin
              → reads NDJSON response stream from stdout
              → formats output for terminal
```

**Key packages:**
- `driver/` — driver discovery (`FindAll`, `Locate`), version probe, NDJSON protocol methods
- `src/cmd/` — Cobra commands (`repl`, `agent`, `ps`, `watch`, `archs`, `completion`)
- `drivers/nanoclaw/` — standalone NanoClaw driver binary (separate Go module)
- `drivers/zepto/` — standalone ZeptoClaw driver binary (separate Go module)

## Driver protocol

Drivers communicate via newline-delimited JSON on stdin/stdout. Request types:
- `version_request` / `version_response` — driver identity and compatibility
- `probe_request` / `probe_response` — auto-detect architecture from a path
- `ps_request` → streams `instance` messages, then `ps_complete`
- `agent_request` → streams `agent_output` chunks, then `agent_complete`
- `watch_request` → streams `message` rows continuously until stdin closes

See `spec/DRIVER.md` for the full protocol spec.

## NanoClaw driver internals

- `db.go` — SQLite helpers: open `store/messages.db`, read groups, read messages, fuzzy group matching
- `ps.go` — queries container runtime (Docker or Apple Containers) + joins with SQLite `registered_groups`; longest-prefix match handles `nanoclaw-<folder>-<timestamp>` style container names
- `agent.go` — resolves group, reads secrets from `.env`, spawns `nanoclaw-agent` container with structured mounts (`/workspace/group`, `/workspace/project`, `/home/node/.claude`), streams output through NDJSON. `--native` bypasses the container and runs the agent-runner via Node.js directly; `--verbose` pipes agent-runner stderr to the terminal.
- `watch.go` — emits historical messages then polls SQLite for new rows; exits on stdin close

Source-dir detection: `NANOCLAW_DIR` env var → walk up from binary → `~/src/nanoclaw`.

## ZeptoClaw driver internals

- `probe.go` — detects ZeptoClaw via `~/.zeptoclaw/config.json` and binary presence; overridable via `ZEPTOCLAW_DIR` / `ZEPTOCLAW_BIN`
- `agent.go` — sends requests to `zeptoclaw agent-stdin` using the gateway IPC format; threads session keys for REPL continuity
- `ps.go` — checks both native `zeptoclaw gateway/daemon` processes and `zeptoclaw-*` containers
- `watch.go` — polls `~/.zeptoclaw/sessions/` for CLI session messages (500ms interval)

## Relationship to other projects

- **molt** (`/Users/ken/src/molt`) — sibling Go CLI for migration between architectures. Same driver discovery pattern, same NDJSON protocol style, same project layout. Copy patterns from here.
- **nanoclaw** (`/Users/ken/src/nanoclaw`) — TypeScript agent service. The `scripts/nanoclaw` Python CLI is the source of truth for container spawning, ps, watch, and history logic that `claw-driver-nanoclaw` ports to Go.
- Container sentinels: `---NANOCLAW_OUTPUT_START---` / `---NANOCLAW_OUTPUT_END---` delimit the JSON result in container stdout.
