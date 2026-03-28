# Plan: Fix `claw watch` Missing Bot Responses

> ~~SHIPPED 2026-03-28~~

> `claw watch` shows only the user side of conversations on Telegram, Signal,
> and other channels that don't self-echo sent messages.

---

## Root Cause

Bot responses only reach the `messages` DB when the channel echoes outbound
messages back as incoming events.

**WhatsApp** self-echoes. Baileys fires `messages.upsert` for every sent
message. `onMessage` is called with `is_bot_message: true`, and
`storeMessage()` writes it to the DB. `claw watch` sees it. ✓

**Telegram / Signal / everything else** do not self-echo. `sendMessage()`
fires the Telegram API (or signal-cli), logs success, and returns. Nothing
calls `onMessage`. Nothing calls `storeMessage`. The `messages` table never
gets a row. `claw watch` sees nothing. ✗

`readMessages` and `readNewMessages` in the claw driver query with no
`is_bot_message` filter — they would show bot messages if the rows existed.
The claw driver is not the bug.

---

## Fix

Two changes, both in nanoclaw:

### 1. Channel interface — `storesSentMessages(): boolean`

Add a capability flag so the bot-response store step is skipped for channels
that self-echo (avoiding double-writes).

```typescript
// src/types.ts
export interface Channel {
  // ... existing methods ...
  /** Returns true if the channel echoes sent messages back via onMessage.
   *  When true, outbound messages will be stored by the echo path and
   *  nanoclaw should not store them explicitly. */
  storesSentMessages?(): boolean;
}
```

Implement in each channel:

| Channel | `storesSentMessages()` | Reason |
|---------|------------------------|--------|
| WhatsApp | `true` | Baileys `messages.upsert` fires for sent messages |
| Telegram | `false` (default / omit) | grammY only fires on incoming updates |
| Signal | `false` | signal-cli send is fire-and-forget |
| Gmail | `false` | sent items are not pushed back |
| Emacs | `false` | local transport, no echo |

WhatsApp already works — it only needs the flag to prevent a double-write.
All other channels need the explicit store added.

### 2. `index.ts` — store bot response after send

In `processGroupMessages`, after the successful `channel.sendMessage()` call:

```typescript
// src/index.ts  (inside the runAgent output callback)
if (text) {
  await channel.sendMessage(chatJid, text);
  outputSentToUser = true;

  // Store bot response so it appears in claw watch / message history.
  // Skip for channels that self-echo (WhatsApp via Baileys messages.upsert)
  // to avoid duplicate rows.
  if (!channel.storesSentMessages?.()) {
    storeMessageDirect({
      id: `bot-${Date.now()}-${chatJid}`,
      chat_jid: chatJid,
      sender: 'bot',
      sender_name: ASSISTANT_NAME,
      content: text,
      timestamp: new Date().toISOString(),
      is_from_me: true,
      is_bot_message: true,
    });
  }
}
```

Same store needed for the task-scheduler path (`task-scheduler.ts` line 191)
and the IPC path (`ipc.ts` line 84) — anywhere `sendMessage` is called with
bot-generated text.

#### Helper to avoid repetition

Factor into a wrapper in `index.ts` (or a new `send.ts`):

```typescript
async function sendAndStore(
  channel: Channel,
  chatJid: string,
  text: string,
): Promise<void> {
  await channel.sendMessage(chatJid, text);
  if (!channel.storesSentMessages?.()) {
    storeMessageDirect({
      id: `bot-${Date.now()}-${chatJid}`,
      chat_jid: chatJid,
      sender: 'bot',
      sender_name: ASSISTANT_NAME,
      content: text,
      timestamp: new Date().toISOString(),
      is_from_me: true,
      is_bot_message: true,
    });
  }
}
```

Replace all `channel.sendMessage(chatJid, text)` calls that carry bot output
with `sendAndStore(channel, chatJid, text)`.

---

## Files Changed

| File | Change |
|------|--------|
| `src/types.ts` | Add optional `storesSentMessages?(): boolean` to `Channel` interface |
| `src/channels/whatsapp.ts` | Implement `storesSentMessages() { return true; }` |
| `src/index.ts` | Add `sendAndStore()` helper; replace bot-output `sendMessage` calls |
| `src/task-scheduler.ts` | Use `sendAndStore` for task result delivery |
| `src/ipc.ts` | Use `sendAndStore` for IPC-triggered sends |

Telegram, Signal, Gmail, Emacs channels need no changes — the interface
default (`undefined` → falsy → explicit store) handles them.

---

## ID collision note

The synthetic ID `bot-${Date.now()}-${chatJid}` is not globally unique if two
messages are sent within the same millisecond to the same chat (unlikely in
practice). Use `crypto.randomUUID().slice(0,8)` as a suffix if stricter
uniqueness is needed. The `messages` table uses `INSERT OR REPLACE` on `id`,
so a collision would silently overwrite — acceptable tradeoff given the
frequency.

---

## Tests

| Test | What it checks |
|------|----------------|
| Unit: `sendAndStore` on mock Telegram channel | Row appears in DB with `is_bot_message = 1` |
| Unit: `sendAndStore` on mock WhatsApp channel | No row written (channel self-echoes) |
| Integration: full message round-trip on Telegram fixture | `readMessages()` returns both user and bot messages |
| Existing watch tests | Still pass (no regression on WA path) |

---

## Scope

This is a nanoclaw fix — no changes to the claw driver or `claw watch` command.
The claw driver's `readMessages` / `readNewMessages` queries are already correct.

---

## Console log tailing (related)

Separately: a collapsible **Logs drawer** in the REPL view would stream raw
container stderr (`WS /ws/logs/:group` → `docker logs -f`). This is the
complement to Watch — Watch shows clean message history, Logs shows the
diagnostic layer. Targeted at Phase 2.5 of `claw-console` (alongside REPL,
not blocking it). See `plans/claw-console.md`.
