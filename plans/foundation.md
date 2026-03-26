# Plan: claw v0.1.0 — Foundation

## Context

`claw` is a universal CLI orchestrator for claw agent architectures (NanoClaw, ZeptoClaw, OpenClaw, etc.). Today each architecture has its own CLI — NanoClaw's is the Python script `scripts/nanoclaw`. The goal is a single `claw` binary that delegates to architecture-specific driver binaries (`claw-driver-nanoclaw`, `claw-driver-zepto`, …) via a NDJSON stdin/stdout protocol — the same pattern molt uses for export/import.

`/Users/ken/src/claw` is currently empty. This plan builds v0.1.0: the Go binary, driver protocol, nanoclaw driver, and four commands (`agent`, `ps`, `watch`, `archs`).

## Reference projects

- **`/Users/ken/src/molt/`** — Go/Cobra CLI with identical driver pattern. Copy heavily from here.
  - `src/cmd/root.go`, `src/cmd/archs.go`, `src/cmd/helpers.go`, `src/cmd/completion.go`
  - `src/driver/driver.go` — driver discovery + NDJSON protocol
  - `drivers/nanoclaw/main.go` — NDJSON dispatch loop pattern
  - `drivers/nanoclaw/db.go` — SQLite helpers (reuse almost verbatim, add `readMessages`)
- **`/Users/ken/src/nanoclaw/scripts/nanoclaw`** — Python source of truth for container spawn payload, ps logic, watch logic, and source-dir detection. Port this logic to Go in `claw-driver-nanoclaw`.

## Architecture

```
claw (Go binary)
  └─ claw-driver-nanoclaw (Go binary, separate module)
       ├─ ps      → docker/container ls + SQLite registered_groups join
       ├─ agent   → spawn nanoclaw-agent container, stream output
       └─ watch   → poll SQLite messages table
```

Driver discovery: `~/.claw/drivers/` then `$PATH`, searching for `claw-driver-*` binaries (same as molt's `~/.molt/drivers/` + `molt-driver-*` pattern).

## Directory structure

```
/Users/ken/src/claw/
├── go.mod                            # module github.com/kenbolton/claw; go 1.22; require github.com/spf13/cobra v1.8.0
├── Makefile                          # copy molt Makefile, substitute molt→claw
├── spec/
│   └── DRIVER.md                     # claw driver protocol spec
├── src/
│   ├── main.go                       # calls cmd.Execute()
│   └── cmd/
│       ├── root.go                   # Cobra root + persistent --arch flag
│       ├── agent.go                  # claw agent [prompt] -g/-j/-s/-f/--pipe
│       ├── ps.go                     # claw ps [--arch] [--json]
│       ├── watch.go                  # claw watch -g/-n
│       ├── archs.go                  # claw archs (copy molt archs.go verbatim)
│       ├── completion.go             # claw completion <bash|zsh|fish>
│       └── helpers.go                # locateDriver(), detectOrFlagArch()
├── driver/
│   └── driver.go                     # copy molt driver.go; strip Export/Import; rename molt→claw prefix
└── drivers/
    └── nanoclaw/
        ├── go.mod                    # module github.com/kenbolton/claw-driver-nanoclaw; require modernc.org/sqlite
        ├── main.go                   # NDJSON dispatch: version/probe/ps/agent/watch
        ├── db.go                     # copy molt drivers/nanoclaw/db.go; add readMessages(), findGroup()
        ├── ps.go                     # handlePs: container runtime query + DB join
        ├── agent.go                  # handleAgent: resolve group, read secrets, spawn container, stream
        └── watch.go                  # handleWatch: history + poll loop + stdin-close exit
```

## Driver protocol (`spec/DRIVER.md`)

All messages: NDJSON on stdin/stdout. `version_request`/`probe_request` identical to molt.

**ps_request:**
```json
{"type": "ps_request", "source_dir": "/path/to/nanoclaw"}
// driver emits zero or more:
{"type": "instance", "id": "nanoclaw-main", "arch": "nanoclaw", "group": "main", "folder": "main", "jid": "...", "state": "running", "age": "3m", "is_main": true}
// then:
{"type": "ps_complete", "warnings": []}
```

**agent_request:**
```json
{"type": "agent_request", "source_dir": "...", "group": "main", "jid": "", "prompt": "...", "session_id": "", "resume_at": ""}
// driver streams:
{"type": "agent_output", "text": "...", "chunk": true}
{"type": "agent_complete", "session_id": "...", "status": "success", "input_tokens": 42, "output_tokens": 11}
```

**watch_request:**
```json
{"type": "watch_request", "source_dir": "...", "group": "main", "jid": "", "lines": 20}
// driver streams continuously:
{"type": "message", "timestamp": "2026-03-26T10:01:33Z", "sender": "You", "content": "...", "is_bot": false}
// exits when orchestrator closes stdin
```

**error:**
```json
{"type": "error", "code": "GROUP_NOT_FOUND", "message": "no group matching 'foo'"}
```

## Key implementation details

### Source-dir detection (in driver)
Same as Python script: `NANOCLAW_DIR` env var → walk up from binary location looking for `store/messages.db` or `.env` → fall back to `~/src/nanoclaw`.

### `claw agent` — container spawning
Port Python `run_container()` to Go in `drivers/nanoclaw/agent.go`:
1. Fuzzy-match group from `registered_groups` (copy `find_group` logic)
2. Read secrets from `<source_dir>/.env` (parse known keys: `ANTHROPIC_API_KEY`, etc.)
3. Detect runtime: try `container` then `docker` via `exec.LookPath`
4. Spawn: `<runtime> run -i --rm nanoclaw-agent:latest` (no volume mounts — stateless mode, same as Python CLI)
5. Write `ContainerInput` JSON to container stdin, close stdin
6. Read stdout line by line; forward as `agent_output` chunks
7. On `---NANOCLAW_OUTPUT_END---`, parse result JSON, emit `agent_complete`
8. Lines starting with `npm notice` are dropped (same as Python script)

### `claw ps` — container listing
In `drivers/nanoclaw/ps.go`:
1. Run `<runtime> ls --format json` (apple) or `docker ps --filter name=nanoclaw- --format json` (docker)
2. Parse JSON, filter for `nanoclaw-*` container IDs
3. Open SQLite, query `registered_groups`; join on container name = `nanoclaw-{folder}`
4. Emit `instance` per container, then `ps_complete`

### `claw watch` — SQLite polling
In `drivers/nanoclaw/watch.go`:
1. Resolve group to JID from DB
2. If `lines > 0`, emit last N messages from `messages WHERE chat_jid = ?`
3. Poll loop (500ms): query `messages WHERE chat_jid = ? AND timestamp > ?`, emit new rows
4. Exit when stdin closes — use a goroutine reading 1 byte from stdin to signal a `done` channel

### `claw archs`
Near-verbatim copy of `molt archs`. `driver.FindAll()` searches `~/.claw/drivers/` then `$PATH` for `claw-driver-*`, calls `version_request`, returns `[]*Driver`.

### Mult-arch `claw ps`
Without `--arch`, iterate all installed drivers with `driver.FindAll()`, call `ps_request` on each, merge `instance` messages into a single table with an `ARCH` column.

## Implementation order

1. `go.mod`, `src/main.go`, `src/cmd/root.go` — basic scaffold
2. `driver/driver.go` — copy molt, rename strings, strip Export/Import
3. `src/cmd/archs.go` + `helpers.go` → **`claw archs` works**
4. `drivers/nanoclaw/go.mod`, `main.go`, `db.go` — driver scaffold + version/probe
5. `drivers/nanoclaw/ps.go` + `src/cmd/ps.go` → **`claw ps` works**
6. `drivers/nanoclaw/agent.go` + `src/cmd/agent.go` → **`claw agent` works**
7. `drivers/nanoclaw/watch.go` + `src/cmd/watch.go` → **`claw watch` works**
8. `spec/DRIVER.md`, `Makefile`, `src/cmd/completion.go`

## Files to copy from molt (with string substitution only)

| Molt source | Claw destination | Change |
|---|---|---|
| `src/main.go` | `src/main.go` | Import path |
| `src/cmd/root.go` | `src/cmd/root.go` | Remove export/import/diff/inspect; update description |
| `src/cmd/archs.go` | `src/cmd/archs.go` | `molt-driver-*` → `claw-driver-*`; `~/.molt` → `~/.claw` |
| `src/cmd/helpers.go` | `src/cmd/helpers.go` | Remove bundle helpers |
| `src/cmd/completion.go` | `src/cmd/completion.go` | Update binary name |
| `src/driver/driver.go` | `driver/driver.go` | Strip Export/Import; `molt-driver-` → `claw-driver-`; `~/.molt` → `~/.claw` |
| `drivers/nanoclaw/go.mod` | `drivers/nanoclaw/go.mod` | Module name |
| `drivers/nanoclaw/db.go` | `drivers/nanoclaw/db.go` | Add `readMessages()`, `findGroup()` |
| `drivers/nanoclaw/main.go` | `drivers/nanoclaw/main.go` | Replace export/import with ps/agent/watch handlers |

## Verification

```bash
# Build both binaries
cd /Users/ken/src/claw && go build -o build/claw ./src
cd drivers/nanoclaw && go build -o ../../build/claw-driver-nanoclaw .

# Copy driver to PATH or ~/.claw/drivers/
cp build/claw-driver-nanoclaw ~/.claw/drivers/

# Smoke test each command
./build/claw archs                        # should show nanoclaw
NANOCLAW_DIR=~/src/nanoclaw ./build/claw ps
NANOCLAW_DIR=~/src/nanoclaw ./build/claw watch -g main -n 5
NANOCLAW_DIR=~/src/nanoclaw ./build/claw agent -g main "What is 2+2?"
```
