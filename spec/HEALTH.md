# claw health — Spec

Health diagnostics across claw agent installations. Runs a set of checks
against one or more installations and reports status per check, per group,
and overall.

---

## Command interface

```
claw health                     Check all installed architectures
claw health --arch nanoclaw     Check a specific architecture only
claw health -g main             Check a specific group within an installation
claw health --watch             Re-run every 30s (configurable with --interval)
claw health --interval 60       Polling interval in seconds (default: 30)
claw health --json              Emit NDJSON instead of formatted output
claw health --fail-fast         Exit 1 on first failed check
```

Global `--arch` flag applies as usual. `--source-dir` overrides auto-detect.

---

## Output

Default (human-readable):

```
nanoclaw  /Users/you/src/nanoclaw
  ✓  runtime         docker 27.3.1
  ✓  credentials     CLAUDE_CODE_OAUTH_TOKEN valid (expires in 47d)
  ✓  database        ok (messages.db 142MB, 18,432 rows)
  ✗  disk            group dir 94% full (/Users/you/src/nanoclaw/groups)
  ✓  sessions        3 active, 0 stuck
  ✓  groups          4 registered (main, dev, family, work)
  ✓  skills          8 skills installed, no conflicts
  ⚠  image           nanoclaw-agent:latest is 12 versions behind

Overall: WARN  (1 error, 1 warning)
```

Symbols: `✓` = pass, `⚠` = warn, `✗` = fail

With `--json`, each check is one NDJSON line followed by a summary:

```json
{"type": "check", "arch": "nanoclaw", "source_dir": "...", "name": "runtime", "status": "pass", "detail": "docker 27.3.1"}
{"type": "check", "arch": "nanoclaw", "source_dir": "...", "name": "credentials", "status": "pass", "detail": "CLAUDE_CODE_OAUTH_TOKEN valid (expires in 47d)"}
{"type": "check", "arch": "nanoclaw", "source_dir": "...", "name": "disk", "status": "fail", "detail": "group dir 94% full"}
{"type": "health_complete", "arch": "nanoclaw", "source_dir": "...", "pass": 5, "warn": 1, "fail": 1}
```

---

## Check catalogue

### runtime
Does the container runtime exist and respond?

- Runs `docker info` or `container info`
- **Pass:** runtime responds, version captured
- **Warn:** runtime found but version is very old (below driver-defined minimum)
- **Fail:** runtime not found or not responding

---

### credentials
Are the API credentials present and valid?

Checks in priority order: `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`.

- **Pass:** token found; if OAuth, expiry is >7 days away
- **Warn:** token found; OAuth expiry is 1–7 days away
- **Fail:** no token found; token present but malformed; OAuth token expired

Does not make a live API call — checks format and expiry only. A `--ping`
flag will optionally make a minimal live request to verify the key works.

---

### database
Is the messages DB accessible and healthy?

- Opens `store/messages.db` read-only
- Runs `PRAGMA integrity_check`
- Captures size and row counts
- **Pass:** integrity ok, size within normal range
- **Warn:** DB > 1GB (may impact performance)
- **Fail:** DB not found, unreadable, or integrity check fails

---

### disk
Is there enough disk space for continued operation?

Checks the group directory and session directory mount points.

- **Pass:** < 80% full
- **Warn:** 80–90% full
- **Fail:** > 90% full

---

### sessions
Are there stuck or zombie agent sessions?

A session is considered stuck if:
- A container is running but has produced no stdout in >10 minutes (container path)
- A node process is running but its PID no longer maps to the session (native path)

- **Pass:** all active sessions look healthy
- **Warn:** 1+ sessions have been running >2 hours (long but not necessarily stuck)
- **Fail:** 1+ sessions appear stuck (running but unresponsive)

Reports count of active sessions regardless of status.

---

### groups
Are the registered groups consistent?

- All registered groups have a directory under `groups/`
- All group directories have a `CLAUDE.md`
- JIDs are non-empty and non-duplicate

- **Pass:** all groups consistent
- **Warn:** a group is registered but missing its directory or `CLAUDE.md`
- **Fail:** duplicate JIDs or DB read error

---

### skills
Are the installed skills consistent across session dirs?

Scans `data/sessions/*/skills/` for conflicts that would cause Claude Code
to behave unpredictably. Two failure modes:

**Tool name collision** — two different skills register the same tool name.
Claude Code's behavior when this happens is undefined; the last-loaded skill
wins, silently dropping the other.

- Scan all skill dirs per group session
- Parse each `SKILL.md` (or `skill.json`) for `tools:` declarations
- Flag any tool name that appears in more than one skill

**Orphaned branch ref** — a session JSONL references a skill tool that no
longer exists in the installed skill set (skill was removed or renamed but
the session wasn't cleared).

- Parse the most recent `.jsonl` session file for each group
- Check all tool_use blocks against currently installed tool names
- Exclude Claude Code built-in tools from the check (these appear in JSONL
  but are not skills): `Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep`,
  `WebFetch`, `WebSearch`, `Task`, `NotebookEdit`, `TodoWrite`,
  `MultiEdit`, `ExitPlanMode`, `EnterPlanMode`, `Agent`
- Flag unresolved tool references (non-built-ins only)

Output example:

```
nanoclaw  /Users/you/src/nanoclaw
  ✗  skills  tool name collision: "Bash" registered by both "clawsec" and "devtools" in group "dev"
  ⚠  skills  orphaned tool ref: "WikiSearch" in dev/.claude/*.jsonl — skill not installed
```

- **Pass:** no collisions, no orphaned refs
- **Warn:** orphaned refs only (session will work but may produce tool-not-found errors)
- **Fail:** tool name collision (one skill silently shadows another)

**Quick fix guidance** (always included on warn/fail):

```
remediation: "rm -rf data/sessions/dev/.claude/skills/<skill-name>/ to remove the conflicting skill"
```

For orphaned refs, the only clean fix is clearing the session:

```
remediation: "rm -rf data/sessions/dev/.claude/ to start a clean session (group memory is preserved)"
```

---

### image
Is the container image up to date? *(container path only)*

- Compares `nanoclaw-agent:latest` digest against the running arch version
- Checks if a newer version is available in the configured registry
- **Pass:** image matches arch version
- **Warn:** image is 1–5 versions behind
- **Fail:** image is >5 versions behind, or image not found locally

---

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | All checks passed |
| 1 | One or more checks failed |
| 2 | One or more warnings, no failures |
| 3 | Could not run checks (driver error, installation not found) |

---

## Driver protocol extension

`claw health` delegates to the driver via a new `health_request` type.
Drivers that don't implement it return `{"type": "error", "code": "UNSUPPORTED"}`;
`claw` falls back to a partial check using only information it can gather
without driver help (runtime detection, disk space).

### health_request

```json
{
  "type": "health_request",
  "source_dir": "/path/to/install",
  "group": "",
  "checks": ["runtime", "credentials", "database", "disk", "sessions", "groups", "skills", "image"]
}
```

- `group` — if non-empty, scope group-level checks to this group only
- `checks` — list of checks to run; omit or send `[]` for all

### health_response stream

Driver emits one message per check result, then a completion:

```json
{"type": "check_result", "name": "runtime",     "status": "pass", "detail": "docker 27.3.1"}
{"type": "check_result", "name": "credentials", "status": "pass", "detail": "CLAUDE_CODE_OAUTH_TOKEN valid (expires in 47d)"}
{"type": "check_result", "name": "disk",        "status": "fail", "detail": "group dir 94% full (/path/to/groups)", "remediation": "Free up space or move groups to a larger volume"}
{"type": "health_complete", "pass": 5, "warn": 1, "fail": 1}
```

Each `check_result` may include:
- `detail` — human-readable description of what was found
- `remediation` — suggested fix (shown on failure/warn)
- `data` — optional structured data for `--json` consumers

### error codes

| Code | Meaning |
|------|---------|
| `UNSUPPORTED` | Driver does not implement health_request |
| `CHECK_ERROR` | A check could not be run (distinct from a failed check) |

---

## Implementation notes

- Checks run in parallel within a driver invocation; overall order of results
  is not guaranteed
- `--watch` mode clears the terminal between runs (ANSI clear); `--json` mode
  appends lines without clearing
- When multiple drivers are installed, results are grouped by driver/installation
- The `sessions` check requires ps-level access; drivers that implement
  `ps_request` can reuse that logic internally
- Credential expiry parsing: OAuth tokens are JWTs; expiry is in the `exp`
  claim. API keys have no expiry — `--ping` is the only way to validate them.
