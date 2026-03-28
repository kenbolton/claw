# Native Pipe — Gap Closing Plan

> Tracked gaps between the current `--native --pipe` implementation and a production-ready scripting experience.

## Gap 1: `--template` flag for prompt wrapping

**Problem:** Piped content always appends after the positional arg with `\n\n`. This works but produces slightly awkward prompts — the instruction comes before the content. For large inputs (long diffs, log files) the model reads the instruction, then wades through content. Some tasks want the instruction after: "Here is the diff:\n\n{content}\n\nWhat changed?"

**Proposed:** Add `--template` / `-t` flag. `{input}` is replaced by the resolved stdin/file content; positional arg is still the fallback instruction.

```bash
git diff | claw agent --native --pipe -g dev \
  --template "Here is a diff:\n\n{input}\n\nSummarise the changes in plain English."
```

**Implementation:** In `resolvePrompt`, if `flagTemplate != ""`, replace `{input}` with the joined non-template parts. Otherwise existing `\n\n` join behaviour is unchanged.

**Files:** `src/cmd/agent.go` (flag + resolvePrompt)

---

## Gap 2: Exit code surfacing

**Problem:** `agent_complete` carries a `status` field (`success` / `error`) and an optional `message`, but the process always exits 0 unless `runAgent` returns an error. Scripts using `&&` chains or `set -e` can't detect agent failures.

**Proposed:** Map `agent_complete.status`:
- `success` → exit 0
- `error` → exit 1
- `timeout` → exit 2 (future)

```bash
git diff | claw agent --native --pipe -g dev "review" || echo "agent failed"
```

**Implementation:** `runAgent` already returns errors from the `error` message type. Extend to also return an error (non-nil) when `status != "success"`. Already handled — just needs the `agent_complete` case to set a sentinel and return it after the scan loop.

**Files:** `src/cmd/agent.go` (runAgent switch)

---

## Gap 3: `--timeout` flag

**Problem:** A hung agent-runner blocks the pipe indefinitely. Native mode has no watchdog (container mode has Docker's own stop mechanism).

**Proposed:** `--timeout <duration>` (default: `5m`). After the deadline, kill the node process and exit with code 2.

```bash
slow_command | claw agent --native --pipe --timeout 30s -g dev "..."
```

**Implementation:** Wrap `cmd.Start()` + scan loop in a goroutine; use `context.WithTimeout` + `cmd.Process.Kill()` on expiry. Write `agent_complete` with `status: "timeout"` before returning.

**Files:** `drivers/nanoclaw/agent.go` (handleAgentNative), `src/cmd/agent.go` (flag)

---

## Gap 4: Structured output mode (`--json`)

**Problem:** Scripting often wants structured data back, not prose. Today the agent's text output is unstructured.

**Proposed:** `--json` flag. Appends a system instruction to the prompt: "Respond with valid JSON only, no prose." Validates and pretty-prints the output; exits 1 if the response isn't valid JSON.

```bash
gh issue list --json title,number \
  | claw agent --native --pipe --json -g dev \
    "for each issue, add a 'priority' field: high/medium/low based on the title"
```

**Implementation:** `resolvePrompt` appends `\n\nRespond with valid JSON only.` when `--json` is set. `runAgent` attempts `json.Unmarshal` on `agent_output` text; if valid, pretty-prints; if not, exits 1.

**Files:** `src/cmd/agent.go`

---

## Gap 5: `--context-files` flag

**Problem:** Many scripting tasks need extra context alongside piped input — a schema file, a style guide, a config — without polluting the session memory. Today you'd have to cat them into the pipe manually.

**Proposed:** `--context-files <glob>` (repeatable). Files are read, labelled with their filename, and prepended to the prompt before stdin content.

```bash
git diff | claw agent --native --pipe -g dev \
  --context-files "docs/style-guide.md" \
  --context-files "api/schema.json" \
  "does this diff follow our style guide and stay within the schema?"
```

**Implementation:** In `resolvePrompt`, after flag parse, glob each `--context-files` pattern, read files, prepend as fenced blocks labelled `# File: <name>`. Cap total context injection at 100KB with a warning.

**Files:** `src/cmd/agent.go`

---

## Gap 6: Native workspace cleanup (`--ephemeral`)

**Problem:** The temp workspace at `/tmp/nanoclaw-native-<folder>/` persists indefinitely. This is intentional for session memory reuse, but sometimes you want a truly stateless invocation (CI, one-off tasks) that doesn't accumulate JSONL.

**Proposed:** `--ephemeral` flag. Uses a unique tmp dir (e.g. `nanoclaw-native-<folder>-<uuid>/`) and removes it after the run. Session memory is not written. No `--session` is valid with `--ephemeral`.

```bash
# CI: completely stateless, no session files left behind
echo "check this PR" | claw agent --native --pipe --ephemeral -g ci
```

**Implementation:** In `handleAgentNative`, if `ephemeral`, generate a uuid suffix for workspaceRoot; `defer os.RemoveAll(workspaceRoot)`. Skip the `claudeDir` symlink (use an empty temp `.claude` dir instead).

**Files:** `drivers/nanoclaw/agent.go` (handleAgentNative), `src/cmd/agent.go` (flag)

---

## Priority Order

| # | Gap | Effort | Value | Ship |
|---|-----|--------|-------|------|
| 2 | Exit code surfacing | XS | high | v0.next |
| 3 | `--timeout` | S | high | v0.next |
| 1 | `--template` | S | medium | v0.next |
| 6 | `--ephemeral` | S | high | v0.next |
| 4 | `--json` output | M | medium | v0.2 |
| 5 | `--context-files` | M | medium | v0.2 |

Gaps 2, 3, 1, 6 are all small and highly complementary — ship together as a batch.
