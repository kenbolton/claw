# Plan: Close Channel Migration Gaps (nanoclaw → zepto) — ✓ SHIPPED 2026-03-27

> Closes the 3 gaps identified in `spec/runbooks/migrate-channel-nanoclaw-to-zepto.md`.
> Each gap is a discrete PR. They can be built in any order.

---

## Gap 1 — `molt export --include <slug>` (molt repo)

**Problem:** To export a single group today you must enumerate every *other* group with `--exclude`. With 10+ groups this is error-prone.

**Change:** Add `--include` (repeatable) to `molt export` and the combined `molt <src> <dst>` shorthand. When `--include` is set, all groups not in the include list are automatically excluded. `--include` and `--exclude` are mutually exclusive.

### Files

**`molt/src/cmd/export.go`**

```go
// Add flag
var exportInclude []string
exportCmd.Flags().StringArrayVar(&exportInclude, "include", nil,
    "Only include this group slug (repeatable; mutually exclusive with --exclude)")

// In runExport, before driver.Export():
if len(exportInclude) > 0 && len(flagExclude) > 0 {
    return fmt.Errorf("--include and --exclude are mutually exclusive")
}
if len(exportInclude) > 0 {
    // resolve all slugs from driver, then exclude everything not in include list
    allSlugs, err := driver.ListSlugs(sourceDir)
    if err != nil {
        return fmt.Errorf("cannot resolve group list for --include: %w", err)
    }
    includeSet := make(map[string]bool, len(exportInclude))
    for _, s := range exportInclude { includeSet[s] = true }
    for _, slug := range allSlugs {
        if !includeSet[slug] { flagExclude = append(flagExclude, slug) }
    }
}
```

**`molt/src/driver/driver.go`** — add `ListSlugs(sourceDir string) ([]string, error)` to the Driver interface.

**`molt/drivers/nanoclaw/main.go`** — implement `ListSlugs`: read `registered_groups` table, return folder names.

### Result

```bash
# Before (pain)
molt export ~/nanoclaw --exclude main --exclude kycsitescan --exclude tanglewylde

# After
molt export ~/nanoclaw --include surf-crew
```

---

## Gap 2 — `molt-driver-zepto` (molt repo)

**Problem:** `molt import <bundle> <dest> --arch zepto` fails — no driver exists.

**What zepto needs from a bundle:**
- `groups/<slug>/CLAUDE.md` and memory files → `$ZEPTO_DIR/groups/<slug>/`
- `groups/<slug>/config.json` → zepto's channel registry (`$ZEPTO_DIR/channels.json`)
- `tasks.json` → zepto's scheduler (`$ZEPTO_DIR/cron/`)
- `sessions/<slug>/` → `$ZEPTO_DIR/sessions/` (format-converted, best-effort)
- Skills → not applicable (zepto uses a different skill system)

**What to build:** `molt/drivers/zepto/` — a new Go binary `molt-driver-zepto`.

### Directory layout

```
molt/drivers/zepto/
  go.mod          (module: github.com/claw-agent-operators/molt/drivers/zepto)
  Makefile        (same pattern as molt/drivers/nanoclaw/Makefile)
  main.go         (NDJSON dispatch loop — same shape as nanoclaw/main.go)
  probe.go        (probeZeptoClaw — copy from claw/drivers/zepto/health.go findDataDir logic)
  export.go       (readGroups, readTasks, exportSessions for zepto layout)
  import.go       (doImport: write CLAUDE.md files, update channels.json, write tasks)
  sessions.go     (best-effort session format conversion)
  util.go         (write/writeError helpers)
```

### Protocol messages to implement

| Message | Notes |
|---------|-------|
| `version_request` | `arch: "zepto"`, `driver_type: "local"` |
| `probe_request` | check `$ZEPTO_DIR/config.json` + binary in PATH |
| `export_request` | read groups from `channels.json`, read session JSON files |
| `import_request` | write group files, update `channels.json`, write tasks to cron dir |

### `probe.go`

```go
func probeZeptoClaw(sourceDir string) float64 {
    dataDir := findDataDir(sourceDir)
    score := 0.0
    if stat, err := os.Stat(filepath.Join(dataDir, "config.json")); err == nil && !stat.IsDir() {
        score += 0.5
    }
    if _, err := os.Stat(filepath.Join(dataDir, "sessions")); err == nil {
        score += 0.2
    }
    if _, err := exec.LookPath("zeptoclaw"); err == nil {
        score += 0.3
    }
    return score
}
```

### `import.go` — key function

```go
func doImport(destDir string, bundle importBundle, renames map[string]string) {
    dataDir := findDataDir(destDir)

    // 1. Write group CLAUDE.md + files
    for _, slug := range bundle.Manifest.Groups {
        destSlug := applyRename(slug, renames)
        groupDir := filepath.Join(dataDir, "groups", destSlug)
        _ = os.MkdirAll(groupDir, 0755)

        claudeKey := fmt.Sprintf("groups/%s/CLAUDE.md", slug)
        if content, ok := bundle.Files[claudeKey]; ok {
            decoded, _ := base64.StdEncoding.DecodeString(content)
            _ = os.WriteFile(filepath.Join(groupDir, "CLAUDE.md"), decoded, 0644)
        }
        // copy other files/ entries similarly
    }

    // 2. Update channels.json with group configs
    updateChannelsJSON(dataDir, bundle, renames)

    // 3. Write tasks to cron dir
    importTasks(dataDir, bundle.Tasks, renames)

    // 4. Sessions — best-effort
    importSessions(dataDir, bundle, renames)

    write(map[string]interface{}{"type": "import_complete", "warnings": []string{}})
}
```

### Session format translation

Nanoclaw session JSONL → Zepto session JSON is lossy but tractable for
the "recent messages" use case:

```
nanoclaw: data/sessions/<folder>/.claude/<session-id>.jsonl
  → array of Claude Code JSONL events (tool use, tool result, message, etc.)

zepto: $ZEPTO_DIR/sessions/<session-key>.json
  → {"messages": [{"role": "user"|"assistant", "content": "..."}]}
```

Converter: scan JSONL for `message` events with `role: user/assistant`, extract
`content[0].text`, write as zepto session JSON. Tool use events are dropped.
Session key: `"<channel>:<jid>"` from the group config.

---

## Gap 3 — Arch dispatch in nanoclaw (nanoclaw repo)

**Problem:** nanoclaw always calls `runContainerAgent()`. There's no way to route a registered group to a different agent runtime (zepto, native, etc.).

**Change:** Add an `arch` column to `registered_groups`. When set, `runAgent()` dispatches to `claw --arch <value>` via the driver protocol instead of calling `runContainerAgent()` directly.

### `db.ts`

```typescript
// Migration (add to existing migration block)
try {
  database.exec(`ALTER TABLE registered_groups ADD COLUMN arch TEXT`);
} catch { /* column already exists */ }
```

Add `arch?: string` to the `RegisteredGroup` type.

### `index.ts` — dispatch fork in `runAgent()`

```typescript
async function runAgent(group: RegisteredGroup, prompt: string, chatJid: string, ...): Promise<'success' | 'error'> {
  // ... existing session/snapshot setup unchanged ...

  if (group.arch && group.arch !== 'nanoclaw') {
    return runClawDriverAgent(group, prompt, chatJid, sessionId, wrappedOnOutput);
  }

  // existing runContainerAgent path unchanged below
  const output = await runContainerAgent(group, { ... });
```

### New function `container-runner.ts` (or `claw-driver-runner.ts`)

```typescript
export async function runClawDriverAgent(
  group: RegisteredGroup,
  prompt: string,
  chatJid: string,
  sessionId: string | undefined,
  onOutput?: (output: ContainerOutput) => Promise<void>,
): Promise<'success' | 'error'> {
  // Locate claw binary
  const clawBin = process.env.CLAW_BIN ?? 'claw';

  // Build agent_request NDJSON
  const req = JSON.stringify({
    type: 'agent_request',
    source_dir: '',
    group: group.folder,
    jid: chatJid,
    prompt,
    session_id: sessionId ?? '',
    resume_at: '',
    native: false,
    verbose: false,
  });

  // Spawn: claw --arch <arch> agent (reading from stdin)
  // Reuse existing NDJSON streaming pattern from container-runner.ts
  // Parse agent_output (stream to onOutput) and agent_complete events
  // Return 'success' | 'error' matching existing contract
}
```

### `mcp_server.ts` / panel — expose arch in group registration UI

The `register_group` MCP tool should accept an optional `arch` field. Groups without `arch` continue to use the container path (default behaviour, fully backward compatible).

### Cut-over in practice

```bash
# Register a group with zepto arch — no restart needed
sqlite3 ~/nanoclaw/store/messages.db \
  "UPDATE registered_groups SET arch = 'zepto' WHERE folder = 'surf-crew';"
```

nanoclaw picks up the change on the next message (no restart needed if the DB is read per-message, which it is).

---

## Build order

```
Gap 1  →  easy, unblocks cleaner single-group exports
Gap 2  →  unblocks automated import; can be built in parallel with Gap 1
Gap 3  →  unblocks live routing without manual DB surgery; depends on nothing
```

All three are independent. Ship Gap 1 + Gap 3 first (lowest risk, highest daily utility).
Gap 2 is a new binary — more test surface, but the structure mirrors nanoclaw 1:1.

## Tests to write

| Gap | Test |
|-----|------|
| 1 | `molt export --include surf-crew` excludes all other groups; `--include` + `--exclude` together errors |
| 2 | roundtrip test: export from zepto fixture → import → verify CLAUDE.md, channels.json, sessions present |
| 2 | probe test: returns 0.0 on empty dir, >0.5 on valid zepto dir |
| 3 | unit: `runAgent` with `group.arch = 'zepto'` calls `runClawDriverAgent`, not `runContainerAgent` |
| 3 | integration: end-to-end with `claw-driver-zepto` stub |
