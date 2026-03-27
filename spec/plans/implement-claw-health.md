# Plan: Implement `claw health`

## Context

The `claw health` command provides health diagnostics across claw agent installations. It runs checks (runtime, credentials, database, disk, sessions, groups, image) against one or more installations and reports status per check. Spec: `spec/HEALTH.md`.

---

## Files to create/modify

| File | Action |
|------|--------|
| `src/cmd/health.go` | **Create** — CLI command, flags, driver dispatch, formatting, fallback, watch loop |
| `src/cmd/health_test.go` | **Create** — Tests for formatting, exit codes, fallback |
| `drivers/nanoclaw/main.go` | **Modify** — Add `health_request` case to switch |
| `drivers/nanoclaw/health.go` | **Create** — 7 check functions using existing helpers |
| `drivers/nanoclaw/health_test.go` | **Create** — Tests for each check |
| `drivers/zepto/main.go` | **Modify** — Add `health_request` case to switch |
| `drivers/zepto/health.go` | **Create** — Applicable checks (runtime, credentials, disk, sessions) |
| `drivers/zepto/health_test.go` | **Create** — Tests for zepto checks |

---

## 1. CLI command (`src/cmd/health.go`)

### Flags
```
--json          NDJSON output (flagHealthJSON bool)
--all           Check all installations (flagHealthAll bool)
-g/--group      Scope to specific group (flagHealthGroup string)
--watch         Re-run on interval (flagHealthWatch bool)
--interval      Polling seconds, default 30 (flagHealthInterval int)
--fail-fast     Exit 1 on first failure (flagHealthFailFast bool)
```
Global `--arch` already on rootCmd.

### Handler flow (`runHealth`)

1. **Resolve drivers** — same pattern as `ps.go`:
   - `--all`: `driver.FindAll()`
   - `--arch`: `locateDriver(flagArch)`
   - Neither: auto-detect via `driver.DetectArch(".")`, fallback to first found

2. **Core logic in `runHealthOnce()`** (extracted for `--watch` reuse):
   - For each driver, send `health_request` via `SendRequestAndClose`
   - Parse `check_result` and `health_complete` messages in scanner loop
   - On `error` with code `UNSUPPORTED`: run fallback checks directly
   - On `--fail-fast` + fail status: break immediately
   - Collect all `checkResult` structs

3. **Output formatting**:
   - Human: header (`arch  source_dir`), each check with `✓`/`⚠`/`✗` + name + detail, `Overall:` summary, remediation hints to stderr
   - `--json`: one NDJSON line per check_result, then health_complete summary line

4. **Exit codes** via `computeExitCode()`:
   - 0 = all pass, 1 = any fail, 2 = warn only, 3 = cannot run

5. **Watch mode**: loop calling `runHealthOnce()`, ANSI clear between runs (not in JSON mode), `time.After(interval)` + signal handling for Ctrl-C

### Fallback checks (when driver returns UNSUPPORTED)
Run directly in CLI process — simpler versions:
- **runtime**: `exec.LookPath("docker")` / `exec.LookPath("container")`, run `docker info`
- **credentials**: check env vars `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`
- **disk**: `syscall.Statfs` on source dir

### Key types
```go
type checkResult struct {
    Arch        string
    SourceDir   string
    Name        string
    Status      string // "pass", "warn", "fail"
    Detail      string
    Remediation string
}
```

---

## 2. Nanoclaw driver handler (`drivers/nanoclaw/health.go`)

Add `case "health_request": handleHealth(msg)` to `main.go` switch.

### `handleHealth(msg)`
- Extract `source_dir`, `group`, `checks` from request
- Default sourceDir via `findSourceDir()`
- Run requested checks (or all if empty), emit `check_result` per check via `write()`
- Emit `health_complete` with tallies

### Check implementations (reusing existing helpers)

| Check | Approach | Reuses |
|-------|----------|--------|
| **runtime** | `detectRuntime()`, then `exec.Command(runtime, "info")` for version | `ps.go:detectRuntime()` |
| **credentials** | `readSecrets(sourceDir)`, check for OAuth/API key, decode JWT `exp` claim for expiry | `db.go:readSecrets()` |
| **database** | `openDB(sourceDir)`, `PRAGMA integrity_check`, `SELECT count(*)`, `os.Stat` for size | `db.go:openDB()` |
| **disk** | `syscall.Statfs` on `groups/` and `data/sessions/` dirs | — |
| **sessions** | `fetchContainers(runtime)`, count running, check for stuck (no stdout in 10min) | `ps.go:detectRuntime()`, `ps.go:fetchContainers()` |
| **groups** | `readGroupRows(sourceDir)`, verify dirs exist, CLAUDE.md present, unique JIDs | `db.go:readGroupRows()` |
| **image** | `docker images nanoclaw-agent:latest`, compare digest against arch version | `db.go:detectArchVersion()` |

### JWT expiry parsing
Base64url-decode second segment of OAuth token, unmarshal `{"exp": <unix_ts>}`, compare to `time.Now()`. Use `encoding/base64.RawURLEncoding`. No external library needed.

---

## 3. Zepto driver handler (`drivers/zepto/health.go`)

Add `case "health_request": handleHealth(msg)` to `main.go` switch.

### Applicable checks
| Check | Approach |
|-------|----------|
| **runtime** | Check for `zeptoclaw` binary and container runtime |
| **credentials** | Check env vars, `~/.zeptoclaw/config.json` for API keys |
| **disk** | `syscall.Statfs` on `~/.zeptoclaw/` |
| **sessions** | Check `~/.zeptoclaw/sessions/` for stale files, check processes |
| **database** | Not applicable — emit pass with "not applicable" |
| **groups** | Not applicable — emit pass with "not applicable" |
| **image** | Check for zeptoclaw container images if applicable |

---

## 4. Tests

### `src/cmd/health_test.go`
- `TestComputeExitCode` — table-driven: all pass→0, has fail→1, warn only→2, empty+error→3
- `TestFormatCheckSymbol` — ✓/⚠/✗ mapping
- `TestFormatHealthOutput` — human-readable output matches expected format
- `TestFormatHealthJSON` — NDJSON lines parse correctly

### `drivers/nanoclaw/health_test.go`
- `TestCheckCredentials` — write temp `.env` with crafted JWT tokens; test expired/near-expiry/valid/missing
- `TestCheckDatabase` — create temp SQLite DB with schema; test pass/missing/large
- `TestCheckGroups` — create temp dirs + DB rows; test consistent/missing-dir/duplicate-JIDs
- `TestCheckDisk` — extract percentage calc into pure function, test thresholds
- `TestCheckRuntime` — test with mocked PATH
- `TestHandleHealth` — end-to-end: capture `write()` output, verify NDJSON stream

### `drivers/zepto/health_test.go`
- Similar but focused on zepto-specific paths (config.json, sessions dir)

### Test helpers
- `testSourceDir(t)` — creates temp dir tree with `store/messages.db`, `groups/`, `.env`
- `createTestDB(t, dir)` — creates minimal SQLite DB with `registered_groups` + `messages` tables

---

## 5. Execution order

1. **Nanoclaw driver** — `health.go` + modify `main.go` + `health_test.go`
2. **Zepto driver** — `health.go` + modify `main.go` + `health_test.go`
3. **CLI command** — `health.go` + `health_test.go`
4. **Verify** — `make build && make build-drivers && make test && make lint`
5. **Smoke test** — `./build/claw health`, `--json`, `--watch`, `--all`, `--fail-fast`

---

## 6. Disk space portability note

Use `syscall.Statfs` (works on darwin + linux). Extract percentage calc: `used = total - free; pct = used * 100 / total`. Keep the pure function testable.
