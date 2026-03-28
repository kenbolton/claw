# ADR: claw-cloudflare — Cloudflare Workers Architecture

**Status:** Proposed
**Date:** 2026-03-27
**Author:** Ken Bolton

---

## Context

The `--native` flag eliminates container overhead for local scripting and CI. A natural question: does this pattern extend to V8 isolates (Cloudflare Workers)?

The short answer is no — and understanding why reveals what a Workers-native claw arch actually needs to look like.

### Why `--native` doesn't port

`--native` works by:
1. Spawning `node dist/index.js` as a subprocess (`exec.LookPath`, `os/exec`)
2. Building a workspace via filesystem symlinks (`os.MkdirAll`, `os.Symlink`)
3. Overriding `HOME` so `~/.claude/` resolves to the per-group session dir

V8 isolates have none of these primitives:
- No subprocess spawning
- No filesystem (no `/tmp`, no symlinks, no persistence)
- No environment variables or `HOME`
- No Node.js standard library

The agent-runner (`index.ts`) is itself a Node.js program saturated with `fs`, `child_process`, and `path` calls — it cannot run inside a Worker.

### What Workers *do* offer

| Constraint | Workers capability |
|---|---|
| No filesystem | KV (low-latency), R2 (blob storage), D1 (SQLite), Durable Objects |
| No subprocess | Fetch API, Service Bindings, Queues |
| No persistent memory | Durable Objects (hibernation), KV with TTL |
| Cold start latency | ~5ms (vs ~2s native, ~10s container) |
| Scale | Automatic, per-request isolation |
| Pricing | Per-request + duration, not per-container |

The Workers model isn't a worse version of `--native` — it's a different execution model optimised for different workloads.

---

## Decision

Define `claw-cloudflare` as a distinct architecture with:

1. A **claw driver** (`claw-driver-cloudflare`) — remote driver type, communicates with a deployed Worker via HTTP+WebSocket instead of stdin/stdout subprocess
2. A **Worker runtime** — edge-compatible agent runner that replaces `agent-runner/src/index.ts`
3. A **molt driver** (`molt-driver-cloudflare`) — remote driver using Cloudflare API for export/import of group state from KV/D1

---

## Architecture

```
claw agent --arch cloudflare -g surf-crew "summarise today"
    │
    ▼
claw-driver-cloudflare (local Go binary)
    │   POST /invoke  (HTTPS)
    ▼
Cloudflare Worker: claw-agent-worker
    ├── Receives prompt + group config
    ├── Loads memory from KV / D1
    ├── Calls Claude API (Anthropic SDK, fetch-based)
    ├── Runs tools (fetch handlers, Workers AI bindings)
    ├── Writes updated memory back to KV / D1
    └── Streams response via SSE or returns JSON
```

### Driver type: remote

```json
{
  "type": "version_response",
  "arch": "cloudflare",
  "driver_type": "remote",
  "requires_config": ["worker_url", "api_token"]
}
```

`source_dir` is always `""` for remote drivers. Config is passed via `~/.claw/config.toml`:

```toml
[arch.cloudflare]
worker_url = "https://claw-agent.your-subdomain.workers.dev"
api_token  = "your-cloudflare-api-token"
```

### Worker runtime

The Worker replaces `agent-runner/index.ts`. Key differences:

| agent-runner (Node.js) | claw-agent-worker (Worker) |
|---|---|
| `fs.readFileSync` CLAUDE.md | `KV.get("group:<slug>:claudemd")` |
| `~/.claude/<session>.jsonl` | `DO.storage.get("session:<id>")` |
| `os.Symlink` for workspace | None needed — all state is remote |
| `child_process.exec` for tools | Fetch handlers / Service Bindings |
| `ANTHROPIC_API_KEY` env var | Cloudflare Secret |
| Runs on host CPU | Runs in V8 isolate, any PoP |

#### Entry point

```typescript
// worker/src/index.ts
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const { prompt, groupSlug, sessionId, ephemeral } = await request.json();

    const memory = await loadMemory(env, groupSlug);
    const session = sessionId ? await loadSession(env, sessionId) : undefined;

    const stream = new TransformStream();
    const writer = stream.writable.getWriter();

    // Run agent in background, stream via SSE
    runAgent({ prompt, memory, session, ephemeral, env, writer });

    return new Response(stream.readable, {
      headers: { 'Content-Type': 'text/event-stream' },
    });
  },
};
```

#### Memory model

```
KV namespace: CLAW_MEMORY
  group:<slug>:claudemd          → CLAUDE.md content
  group:<slug>:files:<path>      → arbitrary memory files

D1 database: CLAW_STATE
  sessions(id, group_slug, started_at, last_active, message_count, summary)
  messages(id, session_id, role, content, timestamp, tool_calls)
  groups(slug, name, jid, trigger, is_main, requires_trigger, added_at)

R2 bucket: CLAW_ARCHIVE
  sessions/<slug>/<session-id>.jsonl   → archived full session transcripts
```

#### Session persistence via Durable Objects

```typescript
// worker/src/session-do.ts
export class SessionDO {
  constructor(private state: DurableObjectState, private env: Env) {}

  async fetch(request: Request): Promise<Response> {
    // Append message, update summary, write to D1
    // Hibernates between turns — no idle cost
  }
}
```

Durable Objects give per-session consistency without a central database bottleneck.

### Tools in Workers

No subprocess means tools are reimplemented as fetch-based handlers:

| nanoclaw tool | Workers equivalent |
|---|---|
| `Bash` | Service Binding to a sandboxed executor Worker (opt-in) |
| `Read`/`Write`/`Edit` | KV / R2 operations scoped to group namespace |
| `WebSearch`/`WebFetch` | Native `fetch()` |
| `mcp__nanoclaw__*` | Workers AI bindings or Service Bindings |
| `Glob`/`Grep` | KV list operations or R2 object listing |

`Bash` is the hard one. Options:
- **Skip it** — most useful agent patterns don't need arbitrary shell execution
- **Sandboxed executor** — separate Worker that accepts shell-like commands, returns output (limited, no real filesystem)
- **Pyodide/WASM** — run a Python or WASM runtime inside the isolate for constrained computation

For v1, skip `Bash`. Stateless agents (research, summarisation, classification, structured output) don't need it.

---

## Sweet spot: stateless one-shot invocations

Workers are not ideal for long-running, tool-heavy sessions. They excel at:

```bash
# Per-webhook agent triage (GitHub, Stripe, etc.)
curl -X POST https://claw-agent.workers.dev/invoke \
  -d '{"group":"ci","prompt":"triage this PR","ephemeral":true}'

# Classification pipeline
cat events.jsonl | while read event; do
  echo "$event" | claw agent --arch cloudflare --ephemeral \
    --template "classify this event as signal/noise: {input}"
done

# Scheduled digest (Cron Trigger)
# worker/src/scheduled.ts: fires daily, loads last 24h of KV data, posts summary
```

The `--ephemeral` flag maps perfectly to stateless Worker invocations — no session, no memory written, pure function.

---

## molt-driver-cloudflare

Remote driver for backup/restore of Cloudflare group state.

```json
{
  "type": "version_response",
  "arch": "cloudflare",
  "driver_type": "remote",
  "requires_config": ["worker_url", "api_token", "account_id", "kv_namespace_id", "d1_database_id"]
}
```

Export: reads KV namespace + D1 tables via Cloudflare REST API, assembles standard `.molt` bundle.
Import: writes CLAUDE.md → KV, registers groups → D1, converts sessions → D1 messages.

This means `molt ~/nanoclaw --include surf-crew workers.dev --arch cloudflare` would migrate a channel from a local nanoclaw instance to Cloudflare in one command.

---

## claw-driver-cloudflare (local binary)

Wraps the remote Worker in the standard driver NDJSON protocol:

```
agent_request → POST /invoke → SSE stream → agent_output chunks → agent_complete
health_request → GET /health → check_result messages
groups_request → GET /groups → group messages
sessions_request → GET /sessions?group=<slug> → session messages
```

Auth: `Authorization: Bearer <api_token>` on all requests.

The local binary is a thin HTTP client. No compute happens locally.

---

## What doesn't map

| nanoclaw feature | cloudflare-claw |
|---|---|
| Long-running container (30min+) | Worker CPU limit: 30s (Paid), 15min (Durable Objects) |
| Arbitrary `Bash` execution | Not available in v1 |
| Real-time `watch` (DB polling) | WebSocket to Durable Object (viable but complex) |
| `--verbose` stderr | Log Tail API (different UX) |
| Skills (branch-merge) | Not applicable — tools are hardcoded or Service Bindings |
| Credential proxy | Not needed — secrets are Cloudflare Secrets, never exposed |

---

## Delivery phases

### Phase 1 — Stateless invoke (v0.1)
- Worker runtime with `WebFetch`, `WebSearch`, `Read`/`Write` via KV
- `claw-driver-cloudflare` local binary: `agent_request` only
- `--ephemeral` mode only (no session persistence yet)
- Target: webhook handlers, classification pipelines, one-shot summarisation

### Phase 2 — Session persistence (v0.2)
- Durable Objects for session state
- `sessions_request` support
- `claw repl --arch cloudflare -g <slug>` works (multi-turn)

### Phase 3 — Full integration (v0.3)
- `molt-driver-cloudflare` for migration + backup
- `health_request` with Cloudflare-specific checks (KV usage, D1 row count, Worker error rate)
- `groups_request` backed by D1
- Cron Triggers for scheduled agents (replaces nanoclaw task scheduler)

---

## Rejected alternatives

**Port agent-runner to edge runtime:** The SDK is too deeply Node.js. An edge-compatible Claude SDK would need to be built from scratch. Too much surface area for v1.

**Run a Node.js Worker (via Node.js compat flag):** Cloudflare's Node.js compat covers most APIs but not `child_process`. The workspace symlink pattern is still broken. Partial solution at best.

**Cloudflare Containers (beta):** Run full Docker containers on Cloudflare's infrastructure. Closer to nanoclaw, but defeats the purpose — this would just be nanoclaw with Cloudflare networking. Interesting for `claw-cloudflare-container` as a separate arch, not this one.

---

## Open questions

1. **Tool extensibility** — how do operators add custom tools without `Bash`? Service Bindings are the answer but require deploying additional Workers per tool. Is that acceptable?
2. **CLAUDE.md hot reload** — KV has eventual consistency. Session context may lag behind a CLAUDE.md update by seconds. Acceptable?
3. **Cost model** — 1M Claude tokens + Workers invocations + D1 reads + KV writes per active group per day. Need a rough estimate before committing to the D1 session model.
4. **`claw repl` latency** — Durable Object hibernation adds ~50–100ms on cold resume. Acceptable for conversational use?
