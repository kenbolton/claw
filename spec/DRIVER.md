# Claw Driver Protocol

Claw drivers are standalone binaries named `claw-driver-<arch>` that communicate with the `claw` CLI via newline-delimited JSON (NDJSON) on stdin/stdout.

## Discovery

Drivers are discovered by searching:
1. `~/.claw/drivers/`
2. `$PATH`

Any executable matching `claw-driver-*` is treated as a candidate driver.

## Message Format

All messages are single-line JSON objects with a `"type"` field.

## Request/Response Types

### version_request / version_response

Used to identify the driver and check compatibility.

```json
{"type": "version_request", "source_dir": "/path/to/install"}
```

```json
{
  "type": "version_response",
  "arch": "nanoclaw",
  "arch_version": "1.2.3",
  "driver_version": "0.1.0",
  "claw_protocol": "0.1.0",
  "driver_type": "local",
  "requires_config": []
}
```

### probe_request / probe_response

Used for auto-detecting architecture from a directory.

```json
{"type": "probe_request", "source_dir": "/path/to/install"}
```

```json
{"type": "probe_response", "arch": "nanoclaw", "confidence": 0.9}
```

### ps_request

List running agent instances.

```json
{"type": "ps_request", "source_dir": "/path/to/install"}
```

Driver emits zero or more instance messages, then a completion:

```json
{"type": "instance", "id": "nanoclaw-main", "arch": "nanoclaw", "group": "main", "folder": "main", "jid": "...", "state": "running", "age": "3m", "is_main": true}
{"type": "ps_complete", "warnings": []}
```

### agent_request

Send a prompt to an agent.

```json
{
  "type": "agent_request",
  "source_dir": "/path/to/install",
  "group": "main",
  "jid": "",
  "prompt": "What is 2+2?",
  "session_id": "",
  "resume_at": "",
  "native": false,
  "verbose": false
}
```

- `native` — run without a container (driver-specific; nanoclaw runs the agent-runner via Node.js). Drivers that don't support native mode may ignore this field.
- `verbose` — pipe agent-runner/container diagnostic stderr to the terminal.

Driver streams output chunks, then a completion:

```json
{"type": "agent_output", "text": "...", "chunk": true}
{"type": "agent_complete", "session_id": "abc123", "status": "success", "input_tokens": 42, "output_tokens": 11}
```

### watch_request

Stream messages from the database in real time.

```json
{
  "type": "watch_request",
  "source_dir": "/path/to/install",
  "group": "main",
  "jid": "",
  "lines": 20
}
```

Driver emits historical messages then polls for new ones:

```json
{"type": "message", "timestamp": "2026-03-26T10:01:33Z", "sender": "You", "content": "Hello", "is_bot": false}
```

The driver exits when the orchestrator closes stdin.

### health_request

Run health diagnostics against an installation.

```json
{
  "type": "health_request",
  "source_dir": "/path/to/install",
  "group": "",
  "checks": ["runtime", "credentials", "database", "disk", "sessions", "groups", "image"]
}
```

- `group` — if non-empty, scope group-level checks to this group only
- `checks` — list of checks to run; omit or send `[]` for all

Driver emits one `check_result` per check, then a completion:

```json
{"type": "check_result", "name": "runtime", "status": "pass", "detail": "docker 27.3.1"}
{"type": "check_result", "name": "disk", "status": "fail", "detail": "group dir 94% full", "remediation": "Free up space or move groups to a larger volume"}
{"type": "health_complete", "pass": 5, "warn": 1, "fail": 1}
```

Status values: `pass`, `warn`, `fail`. Optional `remediation` field provides a suggested fix.

Drivers that don't implement health checks return `{"type": "error", "code": "UNSUPPORTED"}`.

### error

Any request can result in an error:

```json
{"type": "error", "code": "GROUP_NOT_FOUND", "message": "no group matching 'foo'"}
```

## Error Codes

| Code | Meaning |
|------|---------|
| `PARSE_ERROR` | Invalid JSON received |
| `UNKNOWN_TYPE` | Unrecognized message type |
| `MISSING_PROMPT` | agent_request without a prompt |
| `GROUP_NOT_FOUND` | Could not resolve the specified group |
| `NO_RUNTIME` | No container runtime (docker/container) found |
| `SPAWN_ERROR` | Failed to start the agent container or process |
| `DB_ERROR` | Database read error |
| `NATIVE_NO_NODE` | `--native` requested but `node` not found in PATH |
| `NATIVE_NO_DIST` | `--native` requested but agent-runner dist not built |
| `UNSUPPORTED` | Driver does not implement the requested message type |
| `CHECK_ERROR` | A health check could not be run (distinct from a failed check) |

## Lifecycle

1. The orchestrator spawns the driver binary
2. Sends one NDJSON request on stdin
3. Reads NDJSON responses from stdout
4. For `watch_request`: closes stdin to signal the driver to exit
5. For all others: the driver exits after sending the completion message
