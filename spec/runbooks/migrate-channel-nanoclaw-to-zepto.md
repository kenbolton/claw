# Runbook: Migrate a Single Channel — nanoclaw → zeptoclaw

> **Status of automation:**
> - `molt export` (nanoclaw source): ✅ works today
> - `molt import --arch zepto` (zepto dest): ⏳ blocked on `molt-driver-zepto` (not built yet)
> - Memory/file migration: ✅ manual steps below
> - Live message routing cutover: ✅ via `registered_groups` arch field (see Step 6)
>
> This runbook covers the full path today. Automated sections will collapse to a single command once `molt-driver-zepto` ships.

---

## Prerequisites

- `molt` and `molt-driver-nanoclaw` installed and in `$PATH`
- `claw-driver-zepto` installed and in `$PATH`
- Zepto binary (`zeptoclaw`) installed, built, and accessible
- You know the group slug (nanoclaw folder name, e.g. `whatsapp_surf-crew`)
- nanoclaw running and healthy (`claw health`)

---

## Step 0 — Pre-flight

Identify the group slug and verify it exists:

```bash
# Confirm the group slug and JID
sqlite3 ~/nanoclaw/store/messages.db \
  "SELECT key, json_extract(value, '$.name'), json_extract(value, '$.folder')
   FROM registered_groups;"
```

Note the group's `folder` value — this is `<SLUG>` throughout this runbook.
Note the `key` (JID) — this is `<JID>` throughout this runbook.

Get a list of all other slugs (you'll need them for `--exclude`):

```bash
sqlite3 ~/nanoclaw/store/messages.db \
  "SELECT json_extract(value, '$.folder') FROM registered_groups;" \
  | grep -v "<SLUG>"
```

---

## Step 1 — Dry run the export

Verify the export scope before committing:

```bash
molt export ~/nanoclaw \
  --exclude <other-slug-1> \
  --exclude <other-slug-2> \
  --dry-run
```

Expected output:
```
dry-run: would export ~/nanoclaw (arch: nanoclaw) → nanoclaw.molt
  excluded: other-slug-1
  excluded: other-slug-2
```

> **Tip:** If you have many groups, generate the exclude flags automatically:
> ```bash
> slugs=$(sqlite3 ~/nanoclaw/store/messages.db \
>   "SELECT json_extract(value, '$.folder') FROM registered_groups;" \
>   | grep -v "<SLUG>" \
>   | awk '{print "--exclude " $1}' | tr '\n' ' ')
> molt export ~/nanoclaw $slugs --dry-run
> ```

---

## Step 2 — Export the bundle

```bash
molt export ~/nanoclaw \
  --exclude <other-slug-1> \
  --exclude <other-slug-2> \
  --out <SLUG>-$(date +%Y%m%d).molt
```

Inspect what was captured:

```bash
molt inspect <SLUG>-$(date +%Y%m%d).molt
```

Expect:
```
Groups:    1  (<SLUG>)
Tasks:     N
Skills:    N (user-installed)
Sessions:  N files
```

Check for warnings (large files skipped, etc.):
```
⚠  groups/<SLUG>/conversations/...: file exceeds 10 MB limit, skipped
```

If warnings appear, note what was omitted — large session files may need separate handling.

---

## Step 3 — Pause the channel in nanoclaw

Before migrating, prevent nanoclaw from processing new messages for this group.
This avoids a split-brain window where both systems could respond.

**Option A — Trigger guard (preferred):** Temporarily change the trigger to something unused:

```bash
sqlite3 ~/nanoclaw/store/messages.db \
  "UPDATE registered_groups
   SET value = json_set(value, '$.trigger', '@MIGRATING')
   WHERE key = '<JID>';"
```

**Option B — Hard deregister** (if you want a clean break immediately):

```bash
sqlite3 ~/nanoclaw/store/messages.db \
  "DELETE FROM registered_groups WHERE key = '<JID>';"
```

Verify:
```bash
sqlite3 ~/nanoclaw/store/messages.db \
  "SELECT json_extract(value, '$.trigger') FROM registered_groups WHERE key = '<JID>';"
```

---

## Step 4 — Import group memory into zepto

### 4a — Extract the bundle

```bash
# .molt is a gzipped tar
tar -xzf <SLUG>-$(date +%Y%m%d).molt -C /tmp/molt-migrate-<SLUG>/
```

### 4b — Locate zepto's data dir

```bash
# Default: ~/.zeptoclaw/
# Override: $ZEPTOCLAW_DIR
ZEPTO_DIR="${ZEPTOCLAW_DIR:-$HOME/.zeptoclaw}"
```

### 4c — Copy group files

Zepto doesn't have a groups DB, but it reads per-session context. The main
thing to preserve is the `CLAUDE.md` (instructions/memory) and any memory
files the group accumulated.

```bash
mkdir -p "$ZEPTO_DIR/groups/<SLUG>"

# Copy CLAUDE.md
cp /tmp/molt-migrate-<SLUG>/groups/<SLUG>/CLAUDE.md \
   "$ZEPTO_DIR/groups/<SLUG>/CLAUDE.md"

# Copy any other memory files (conversations, custom files)
cp -r /tmp/molt-migrate-<SLUG>/groups/<SLUG>/files/ \
      "$ZEPTO_DIR/groups/<SLUG>/files/" 2>/dev/null || true
```

### 4d — Session history (best-effort)

Nanoclaw sessions are Claude Code JSONL files. Zepto uses its own JSON format
(`~/.zeptoclaw/sessions/<key>.json`). The formats are not directly compatible.

**Option A — Skip session history** (cleanest): zepto starts fresh with the group's
`CLAUDE.md` context. The agent remembers its instructions; only conversational
history is lost.

**Option B — Convert (manual, complex):** Extract message content from the nanoclaw
JSONL and write zepto-compatible session JSON. Not worth doing for most migrations —
CLAUDE.md carries the important state.

For most groups, Option A is correct.

---

## Step 5 — Configure zepto for the channel

Zepto's group config lives in `$ZEPTO_DIR/groups/<SLUG>/config.json`.
This controls how the group is registered and what JID/trigger it responds to.

```bash
cat > "$ZEPTO_DIR/groups/<SLUG>/config.json" << 'EOF'
{
  "slug": "<SLUG>",
  "name": "<Human Name>",
  "jid": "<JID>",
  "trigger": "@Andy",
  "requires_trigger": true,
  "is_main": false
}
EOF
```

> Copy the `jid`, `name`, and `trigger` values from the nanoclaw registered_groups entry you inspected in Step 0.

---

## Step 6 — Route messages to zepto

This is the live cutover. How you do it depends on your deployment:

### 6a — If nanoclaw dispatches to multiple arch drivers (arch field in registered_groups)

Add an `arch` field to the group's registered_groups entry:

```bash
sqlite3 ~/nanoclaw/store/messages.db \
  "UPDATE registered_groups
   SET value = json_set(value, '$.arch', 'zepto', '$.trigger', '@Andy')
   WHERE key = '<JID>';"
```

nanoclaw will then dispatch agent requests to `claw-driver-zepto` instead of running
the Claude Code container. This is the cleanest path — message routing stays with nanoclaw,
only the agent runtime changes.

> **Note:** This requires nanoclaw to read the `arch` field and route accordingly.
> Verify your nanoclaw version supports this before relying on it.

### 6b — If zepto is a separate bot identity

If zepto runs on a different bot account (different phone number, different Telegram bot, etc.):
1. Add the group to zepto's registered groups directly (Step 5 above is sufficient)
2. In the messaging platform, add the zepto bot to the group
3. Remove the nanoclaw bot from the group (or leave both during a parallel-run window)

### 6c — Parallel run (validation window)

Keep nanoclaw registered but silenced (`@MIGRATING` trigger from Step 3).
Send a test message using zepto's trigger and verify the response.
Once confident, complete the nanoclaw deregister (Step 7).

---

## Step 7 — Smoke test

```bash
# Verify zepto can serve the group
claw agent --arch zepto -g <SLUG> "what do you know about this group?"
```

Expected: response referencing the group's CLAUDE.md content (instructions, context, etc.).

```bash
# Check sessions are persisting
claw agent --arch zepto -g <SLUG> "remember that the migration date was $(date +%Y-%m-%d)"
claw agent --arch zepto -g <SLUG> "what was the migration date?"
```

---

## Step 8 — Complete the nanoclaw cutover

Once smoke test passes, finalize deregistration from nanoclaw:

```bash
# Deregister from nanoclaw's routing table
sqlite3 ~/nanoclaw/store/messages.db \
  "DELETE FROM registered_groups WHERE key = '<JID>';"
```

The group's files remain in `~/nanoclaw/groups/<SLUG>/` — don't delete them.
They serve as a cold backup until you're confident the migration is stable.

---

## Step 9 — Archive the bundle

Keep the `.molt` bundle as a point-in-time snapshot:

```bash
mv <SLUG>-$(date +%Y%m%d).molt ~/nanoclaw/backups/
```

---

## Rollback

If something goes wrong before Step 8:

```bash
# Restore nanoclaw routing (re-register with original trigger)
sqlite3 ~/nanoclaw/store/messages.db \
  "UPDATE registered_groups
   SET value = json_set(value, '$.trigger', '@Andy')
   WHERE key = '<JID>';"
```

If you already completed Step 8 (deleted the registered_groups entry):

```bash
# Re-insert from backup
# Replace values with actuals from Step 0
sqlite3 ~/nanoclaw/store/messages.db \
  "INSERT OR REPLACE INTO registered_groups(key, value) VALUES(
    '<JID>',
    json_object(
      'name', '<Human Name>',
      'folder', '<SLUG>',
      'trigger', '@Andy',
      'added_at', '<original-added_at>'
    )
  );"
```

---

## What Comes Next (Automation Gaps)

| Gap | Status | Impact |
|-----|--------|--------|
| `molt-driver-zepto` | Not built | Steps 4a–4d collapse to `molt import <bundle> ~/zepto --arch zepto` |
| `molt export --include <slug>` | Not built | Currently must enumerate `--exclude` for every other group |
| Arch field in nanoclaw routing | Not built | Step 6a requires code change to nanoclaw message dispatcher |
| Session format converter (nano→zepto) | Not planned | Step 4d remains best-effort |

Track these in the molt and nanoclaw backlogs. The only hard manual step long-term is session history conversion — that's a format incompatibility, not a tooling gap.
