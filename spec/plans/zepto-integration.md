# Plan: ZeptoClaw Integration — ✓ SHIPPED 2026-03-27

> Three integration points: live message routing, migration tooling, and export UX.

---

## Part 1 — Arch Dispatch in nanoclaw

> Closes Gap 3 from `spec/plans/migrate-channel-gaps.md`

When a group has `arch: "zepto"` set, `runAgent()` dispatches to `claw --arch zepto`
instead of `runContainerAgent()`. This is the live routing cutover.

### 2a. DB migration

```typescript
// nanoclaw/src/db.ts — add to migration block
try {
  database.exec(`ALTER TABLE registered_groups ADD COLUMN arch TEXT`);
} catch { /* already exists */ }
```

Add `arch?: string` to `RegisteredGroup` type.

### 2b. `runAgent()` dispatch fork

```typescript
// nanoclaw/src/index.ts
async function runAgent(group: RegisteredGroup, prompt: string, chatJid: string, ...): Promise<'success' | 'error'> {
  // ... existing session/snapshot setup (unchanged) ...

  if (group.arch && group.arch !== 'nanoclaw') {
    return runClawDriverAgent(group, prompt, chatJid, sessionId, wrappedOnOutput);
  }

  // existing container path unchanged below
}
```

### 2c. `runClawDriverAgent()` — new function in `container-runner.ts`

```typescript
export async function runClawDriverAgent(
  group: RegisteredGroup,
  prompt: string,
  chatJid: string,
  sessionId: string | undefined,
  onOutput?: (output: ContainerOutput) => Promise<void>,
): Promise<'success' | 'error'> {
  const clawBin = process.env.CLAW_BIN ?? 'claw';

  const req = JSON.stringify({
    type: 'agent_request',
    source_dir: '',
    group: group.folder,
    jid: chatJid,
    prompt,
    session_id: sessionId ?? '',
    resume_at: 'latest',
    native: false,
    verbose: false,
    timeout: '5m',
    ephemeral: false,
  });

  return new Promise((resolve) => {
    const proc = spawn(clawBin, ['--arch', group.arch!, 'agent'], {
      env: { ...process.env },
    });

    // Feed the agent_request via stdin (driver protocol)
    // OR use claw's --pipe mode to pass the prompt directly:
    // spawn(clawBin, ['agent', '--arch', group.arch!, '--pipe', '-g', group.folder])

    // Stream agent_output messages to onOutput
    // Resolve on agent_complete
  });
}
```

**Implementation note:** The simpler path is to call `claw agent --arch zepto --pipe -g <folder>`
and feed the prompt via stdin — avoids speaking the driver NDJSON protocol directly and reuses
all the flag handling and output parsing already built into `claw agent`.

```typescript
const proc = spawn(clawBin, [
  'agent', '--arch', group.arch!,
  '--pipe', '-g', group.folder,
  '--timeout', '5m',
], { env: { ...process.env } });

proc.stdin.write(prompt);
proc.stdin.end();
// parse stdout as text output, stderr for session ID
```

### 2d. MCP tool update — expose `arch` in `register_group`

```typescript
// mcp_server.ts register_group tool
{
  name: 'register_group',
  inputSchema: {
    // ... existing fields ...
    arch: { type: 'string', enum: ['nanoclaw', 'zepto'], default: 'nanoclaw',
            description: 'Agent runtime to use for this group' },
  }
}
```

### 2e. Panel UI

Show `arch` badge on group cards in the nanoclaw panel. Groups running zepto get a
`zepto` pill. Edit form includes arch selector.

---

## Part 2 — `molt-driver-zepto`

> Closes Gap 2 from `spec/plans/migrate-channel-gaps.md`

Full spec in `migrate-channel-gaps.md`. Summary of files:

```
molt/drivers/zepto/
  go.mod
  Makefile
  main.go       NDJSON dispatch
  probe.go      probeZeptoClaw() — check $ZEPTO_DIR/config.json + binary
  export.go     read channels.json, session .json files, tasks from cron/
  import.go     write CLAUDE.md + files, update channels.json, write tasks
  sessions.go   nanoclaw JSONL → zepto JSON (best-effort, messages only)
  util.go
```

Key design decisions:
- `dest_dir` for zepto = `$ZEPTOCLAW_DIR` (defaults to `~/.zeptoclaw`)
- No groups DB — channel registry lives in `channels.json`
- Sessions are best-effort converted (tool calls dropped, messages kept)
- Skills: not applicable (zepto skill system differs; skip with warning)

---

## Part 3 — `molt export --include`

> Closes Gap 1 from `spec/plans/migrate-channel-gaps.md`

Three-line change to `molt/src/cmd/export.go`. Also add `--include` to the
combined `molt <src> <dst>` shorthand. Add `ListSlugs()` to driver interface.

---

## Delivery order

```
Part 3   molt export --include    ← small, unblocks cleaner migration
Part 1   Arch dispatch            ← live routing cutover
Part 2   molt-driver-zepto        ← migration, parallel with Part 1
```

---

## What the world looks like when this ships

```bash
# Register a group with zepto arch
register_group jid="..." name="Surf Crew" folder="whatsapp_surf-crew" arch="zepto"

# Incoming message → nanoclaw → sees arch="zepto" → claw agent --arch zepto --pipe
# Response sent back through nanoclaw's outbound channel — zero change to platform layer

# Migrate a different group from nanoclaw to zepto
molt export ~/nanoclaw --include surf-crew --out surf-crew.molt
molt import surf-crew.molt ~/.zeptoclaw --arch zepto
```
