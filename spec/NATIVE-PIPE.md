# `claw agent --native --pipe`

> Containerless, memory-persistent agent invocations for scripting and automation.

## What It Is

`--native` runs the agent-runner directly via Node.js — no container pull, no container startup, no container daemon. `--pipe` reads the prompt from stdin. Together they make `claw agent` a first-class Unix filter.

```
git diff --staged | claw agent --native --pipe -g dev "review this diff"
```

Response in ~2 seconds. Session memory persists across calls, exactly like containerized runs.

---

## How It Works

`handleAgentNative` builds a temp workspace by symlinking the real data dirs:

```
/tmp/nanoclaw-native-<folder>/
  group/    → <source>/groups/<folder>/           # CLAUDE.md, memory files
  project/  → <source>/                           # DB, registered_groups
  claude/   → <source>/data/sessions/<folder>/.claude/   # session JSONL
```

`HOME` is overridden to the temp root so Node resolves `~/.claude` to the per-group session dir — the same dir that container mode mounts. Session files accumulate across every native call exactly as they do in containerized runs.

Secrets are injected as env vars. `NANOCLAW_WORKSPACE_ROOT` tells agent-runner where to find its paths instead of the hardcoded `/workspace`.

---

## Examples

### Basic questions
```bash
claw agent --native "what's the capital of France?"
```

### Diff review
```bash
git diff --staged | claw agent --native --pipe -g dev "review this diff, flag anything risky"
```

### Log triage
```bash
tail -n 100 /var/log/app.log | claw agent --native --pipe "any errors worth worrying about?"
```

### Code generation into a file
```bash
echo "write a Go function that retries an HTTP GET up to 3 times with exponential backoff" \
  | claw agent --native --pipe -g dev > retry.go
```

### GitHub issue triage
```bash
gh issue view 42 | claw agent --native --pipe \
  "suggest a label: bug, enhancement, docs, or question"
```

### Memory persists across calls
```bash
# Turn 1
echo "my favourite framework is Hono" | claw agent --native --pipe -g dev

# Turn 2 — different process, no container, but it remembers
echo "what's my favourite framework?" | claw agent --native --pipe -g dev
# → "Hono"
```

Session ID is printed to stderr after each run:
```
[session: abc123def456]
```

Resume explicitly:
```bash
claw agent --native -g dev -s abc123def456 "continue from where we left off"
```

### Verbose diagnostics
```bash
git diff | claw agent --native --pipe -g dev --verbose "what changed?" 2>agent-debug.log
```

---

## Scripting Patterns

### Nightly commit summary
```bash
#!/bin/bash
git log --since="24 hours ago" --oneline \
  | claw agent --native --pipe -g dev "summarise these commits in 2 sentences"
```

### Pre-commit hook
```bash
#!/bin/bash
# .git/hooks/pre-commit
staged=$(git diff --cached --name-only)
if [ -n "$staged" ]; then
  result=$(git diff --cached | claw agent --native --pipe -g dev \
    "quick check: any obvious bugs or security issues? reply OK or list concerns")
  echo "$result"
  echo "$result" | grep -qi "^ok$" || exit 1
fi
```

### CI step (no daemon, no container pull)
```yaml
- name: AI code review
  run: |
    git diff ${{ github.event.before }}..HEAD \
      | claw agent --native --pipe -g ci \
        "flag any security issues, breaking API changes, or missing tests"
```

### Pipe chaining
```bash
# Summarise → post
SUMMARY=$(git log --since="24h" --oneline \
  | claw agent --native --pipe -g dev "2-sentence commit summary")

echo "$SUMMARY" | claw agent --native --pipe -g main \
  "post this as a daily team update"
```

---

## Prompt Composition

Multiple input sources are joined with `\n\n`:

| Flag | Content |
|------|---------|
| positional arg | first |
| `-f <file>` | second |
| `--pipe` (stdin) | third |

Example — instructions + piped content:
```bash
git diff | claw agent --native --pipe -g dev "review this diff"
# Sends: "review this diff\n\n<diff content>"
```

All three can be combined:
```bash
git diff | claw agent --native --pipe -f checklist.txt -g dev "also check for:"
# Sends: "also check for:\n\n<checklist content>\n\n<diff content>"
```

---

## When to Use Native vs Container

| | `--native` | container |
|---|---|---|
| Startup time | ~2s | ~10s (warm) / ~30s (cold pull) |
| Sandbox | none | full OCI isolation |
| Memory | shared session dir | same shared session dir |
| Tool access | host filesystem | container filesystem |
| Best for | scripting, CI, dev iteration | production, untrusted prompts |

---

## Flags Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--native` | | Run via Node.js instead of container |
| `--pipe` | | Read prompt from stdin |
| `--timeout` | `5m` | Kill agent after this duration (e.g. `30s`, `5m`) |
| `--template` | | Prompt template — `{input}` replaced by stdin/file content |
| `--ephemeral` | | Disposable workspace, no session persistence |
| `--verbose` | | Pipe agent-runner stderr to terminal |

Exit codes: `0` success, `1` error, `2` timeout.

---

## Remaining Gaps

See `spec/plans/native-pipe-gaps.md` for gaps 4 (`--json`) and 5 (`--context-files`).
