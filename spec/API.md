# claw api — Spec

`claw api` is a lightweight HTTP + WebSocket server that exposes the driver
protocol over the network. It is a thin translation layer: NDJSON over a
subprocess becomes JSON over HTTP/WebSocket. No business logic lives here —
all intelligence stays in the drivers.

Its primary consumer is `claw-console` (the web dashboard), but any HTTP
client can use it.

---

## Starting the server

```
claw api serve                         # localhost:7474, all installed drivers
claw api serve --port 8080             # custom port
claw api serve --bind 0.0.0.0          # expose on all interfaces (requires --token)
claw api serve --token <secret>        # enable bearer token auth
claw api serve --arch nanoclaw         # limit to one architecture
claw api serve --source-dir ~/nanoclaw # target a specific installation
```

Binds `127.0.0.1` by default. Attempting `--bind 0.0.0.0` without `--token`
is a hard error — no silent remote exposure.

---

## Authentication

When `--token` is set, every request must include:

```
Authorization: Bearer <token>
```

WebSocket connections pass the token as a query param:
`ws://localhost:7474/ws/watch/main?token=<secret>`

Requests without a valid token receive `401 Unauthorized`.

Without `--token`, the server accepts all requests (localhost-only use case).

---

## REST endpoints

All responses are `application/json`.

### `GET /api/v1/health`

Run health checks across all installations (or the targeted one).

Query params:
- `arch` — filter to one architecture
- `group` — scope group-level checks to one group
- `checks` — comma-separated list of check names to run (default: all)

Response:

```json
{
  "installations": [
    {
      "arch": "nanoclaw",
      "source_dir": "/Users/you/src/nanoclaw",
      "checks": [
        {"name": "runtime",     "status": "pass", "detail": "docker 27.3.1"},
        {"name": "credentials", "status": "pass", "detail": "CLAUDE_CODE_OAUTH_TOKEN valid (expires in 47d)"},
        {"name": "disk",        "status": "fail", "detail": "group dir 94% full", "remediation": "Free up space or move groups to a larger volume"}
      ],
      "summary": {"pass": 5, "warn": 1, "fail": 1},
      "overall": "fail"
    }
  ]
}
```

### `GET /api/v1/ps`

List running agent instances across all drivers.

Query params:
- `arch` — filter to one architecture

Response:

```json
{
  "instances": [
    {
      "id": "nanoclaw-main",
      "arch": "nanoclaw",
      "group": "main",
      "folder": "main",
      "jid": "...",
      "state": "running",
      "age": "3m",
      "is_main": true
    }
  ]
}
```

### `GET /api/v1/groups`

List registered groups for all installations.

Query params:
- `arch` — filter to one architecture

Response:

```json
{
  "groups": [
    {
      "arch": "nanoclaw",
      "source_dir": "/Users/you/src/nanoclaw",
      "jid": "...",
      "name": "main",
      "folder": "main",
      "trigger": "@Andy",
      "is_main": true,
      "requires_trigger": false
    }
  ]
}
```

### `GET /api/v1/sessions`

List recent sessions for a group.

Query params:
- `arch` (required)
- `group` (required)
- `limit` — max results (default: 50)

Response:

```json
{
  "sessions": [
    {
      "session_id": "abc123",
      "group": "main",
      "started_at": "2026-03-27T09:00:00Z",
      "last_active": "2026-03-27T09:45:00Z",
      "message_count": 12,
      "summary": "Reviewed deploy config and ran health checks"
    }
  ]
}
```

### `GET /api/v1/archs`

List installed drivers.

Response:

```json
{
  "drivers": [
    {
      "arch": "nanoclaw",
      "arch_version": "1.2.35",
      "driver_version": "0.1.0",
      "driver_type": "local",
      "path": "/Users/you/.claw/drivers/claw-driver-nanoclaw"
    }
  ]
}
```

---

## WebSocket endpoints

WebSocket connections stream NDJSON. Each message is one JSON object per line.

### `WS /ws/watch/:group`

Stream messages from a group in real time. Mirrors `watch_request`.

Query params:
- `arch` — target architecture (default: auto-detect)
- `lines` — history lines on connect (default: 20)
- `token` — bearer token (if auth enabled)

Server sends:

```json
{"type": "message", "timestamp": "2026-03-27T09:00:00Z", "sender": "You", "content": "Hello", "is_bot": false}
{"type": "message", "timestamp": "2026-03-27T09:00:04Z", "sender": "Andy", "content": "Hi!", "is_bot": true}
```

Connection stays open until client disconnects.

### `WS /ws/agent/:group`

Send a prompt and stream the response. Mirrors `agent_request`.

Query params:
- `arch` — target architecture
- `session` — session ID to resume
- `native` — run without container (`true`/`false`)
- `token` — bearer token

Client sends one message to start:

```json
{"prompt": "What's the status of the deploy?"}
```

Server streams:

```json
{"type": "agent_output", "text": "The deploy completed...", "chunk": true}
{"type": "agent_output", "text": " successfully at 13:58.", "chunk": true}
{"type": "agent_complete", "session_id": "abc123", "status": "success", "input_tokens": 412, "output_tokens": 89}
```

Client can send additional prompts on the same connection to continue the
conversation (session threading is maintained server-side for the lifetime
of the WebSocket connection).

### `WS /ws/health`

Stream live health check results. Re-runs all checks on a configurable interval.

Query params:
- `interval` — seconds between full re-runs (default: 30)
- `token` — bearer token

Server sends check results as they complete (not batched):

```json
{"type": "check", "arch": "nanoclaw", "name": "runtime", "status": "pass", "detail": "docker 27.3.1"}
{"type": "check", "arch": "nanoclaw", "name": "disk",    "status": "fail", "detail": "group dir 94% full"}
{"type": "health_complete", "arch": "nanoclaw", "pass": 5, "warn": 1, "fail": 1, "ts": "2026-03-27T09:00:00Z"}
```

---

## Error responses

REST errors:

```json
{"error": "GROUP_NOT_FOUND", "message": "no group matching 'foo'"}
```

HTTP status codes follow standard conventions: 400 bad request, 401 auth
required, 404 not found, 500 driver error.

WebSocket errors are sent as a message before closing:

```json
{"type": "error", "code": "DRIVER_ERROR", "message": "claw-driver-nanoclaw exited unexpectedly"}
```

---

## CORS

Allowed origins default to `http://localhost:*` (for `claw-console` dev).
`--cors-origin <origin>` adds additional allowed origins for production
console deployments.

---

## Implementation notes

- The server is a subcommand of the existing `claw` binary — no separate
  binary needed. `claw api serve` starts it.
- Implementation lives in the `api/` package (`server.go`, `middleware.go`,
  `fanout.go`, `errors.go`, `rest_*.go`, `ws_*.go`). Cobra wiring is in
  `src/cmd/api.go`.
- Uses Go 1.23's `net/http.ServeMux` with native path parameters and
  `github.com/coder/websocket` for WebSocket support. No external framework.
- Each WebSocket connection that requires a driver spawns one driver
  subprocess. The watch connection holds the subprocess alive until
  disconnect; agent connections spawn per-prompt (matching CLI behavior).
- `GET /api/v1/ps` and `GET /api/v1/health` fan out to all installed
  drivers concurrently (via `fanout.go`) and merge results.
- The server does not cache driver state — every REST request invokes the
  driver fresh. WebSocket streams hold the subprocess open.
- Two new driver protocol messages (`groups_request`, `sessions_request`)
  were added to support the groups and sessions endpoints. See
  `spec/DRIVER.md`.

---

## `claw-console` (companion dashboard)

Separate repo in the org. A web UI that connects to `claw api`.

**Stack:** Vite + React + TypeScript. No backend of its own — `claw api` is
the backend.

**Views:**

| View | Data source |
|------|-------------|
| Health | `WS /ws/health` — live tiles per installation |
| Agents | `GET /api/v1/ps` + polling or `WS /ws/health` for state changes |
| Watch | `WS /ws/watch/:group` — live message feed, group picker |
| REPL | `WS /ws/agent/:group` — full browser REPL with session continuity |
| Sessions | `GET /api/v1/sessions` — searchable history, click to resume in REPL |
| Groups | `GET /api/v1/groups` — config viewer |

**Distribution:** ships as a static build. `claw api serve --console` serves
it from the same port (embedded via Go embed). Or run it separately pointing
at a remote `claw api` instance.
