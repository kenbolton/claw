# Plan: claw-console

> ~~SHIPPED 2026-03-28~~ — all three phases live in `extra/src/claw-console/`
> Built dist embedded in `claw/console/` via `//go:embed dist/*`
>
> **Remaining:** `jid.ts` + `TransportBadge` not yet in source — `GroupCard`
> renders raw JID string. Low priority follow-up.

> Web dashboard for `claw api`. Vite + React + TypeScript.
> Connects to `claw api serve` — no backend of its own.

---

## On json-render

`vercel-labs/json-render` solves a specific problem: an LLM generates a JSON
spec describing a UI, and the framework renders it safely using a pre-approved
component catalog. It's built on shadcn/ui and supports React, Vue, React
Native, etc.

**Where it fits for claw-console:**

The health view is a natural match. `WS /ws/health` streams structured check
results — the server already knows the layout (status tiles, remediation
strings, pass/warn/fail counts). Wrapping those in json-render specs means the
Go server controls presentation, the frontend just renders. Adding a new check
type or a driver-specific remediation widget means updating `claw api`, not the
React app.

**Where it doesn't fit:**

The REPL, Watch feed, and Sessions views require direct React — WebSocket
streaming, interactive input, optimistic updates, and scroll behaviour don't
map cleanly to a spec-driven renderer. These should be standard React
components.

**Recommended approach:**

Use json-render for **health tiles only**. Use shadcn/ui directly (the same
component library json-render is built on) for everything else. This gets the
benefits (driver-controlled health UI) without forcing the entire dashboard
through an indirection layer.

---

## Stack

```
claw-console/
├── Vite 6 + React 19 + TypeScript 5.x
├── shadcn/ui (Tailwind CSS, Radix primitives)
├── @json-render/core + @json-render/react  ← health view only
├── Zustand (WebSocket connection state)
├── React Query (REST endpoint polling)
└── No backend — talks directly to claw api
```

---

## Directory layout

```
claw-console/
├── index.html
├── vite.config.ts
├── tailwind.config.ts
├── src/
│   ├── main.tsx
│   ├── App.tsx                     # root layout, routing
│   ├── lib/
│   │   ├── api.ts                  # fetch wrappers for REST endpoints
│   │   ├── ws.ts                   # WebSocket connection factory
│   │   ├── config.ts               # base URL, token from localStorage
│   │   └── jid.ts                  # transportFromJID(), Transport type
│   ├── store/
│   │   ├── connection.ts           # Zustand: api URL, token, connection state
│   │   └── health.ts               # Zustand: live health check state
│   ├── components/
│   │   ├── ui/                     # shadcn/ui generated components
│   │   ├── health/
│   │   │   ├── HealthBoard.tsx     # WS /ws/health consumer
│   │   │   ├── HealthTile.tsx      # per-check card (json-render output)
│   │   │   ├── catalog.ts          # json-render catalog (CheckResult, StatusBadge, Remediation)
│   │   │   └── registry.tsx        # json-render registry (maps to shadcn/ui)
│   │   ├── agents/
│   │   │   ├── AgentList.tsx       # GET /api/v1/ps, auto-refresh
│   │   │   └── AgentRow.tsx
│   │   ├── watch/
│   │   │   ├── WatchFeed.tsx       # WS /ws/watch/:group
│   │   │   ├── MessageBubble.tsx
│   │   │   └── GroupPicker.tsx
│   │   ├── repl/
│   │   │   ├── Repl.tsx            # WS /ws/agent/:group
│   │   │   ├── ReplInput.tsx
│   │   │   ├── ReplMessage.tsx
│   │   │   └── SessionBadge.tsx
│   │   ├── sessions/
│   │   │   ├── SessionList.tsx     # GET /api/v1/sessions
│   │   │   └── SessionRow.tsx
│   │   ├── groups/
│   │   │   ├── GroupList.tsx       # GET /api/v1/groups
│   │   │   └── GroupCard.tsx
│   │   ├── common/
│   │   │   └── TransportBadge.tsx  # WhatsApp/Telegram/Signal/Discord pill
│   │   └── layout/
│   │       ├── Sidebar.tsx
│   │       ├── TopBar.tsx
│   │       └── ConnectModal.tsx    # API URL + token input, stored in localStorage
│   └── views/
│       ├── HealthView.tsx
│       ├── AgentsView.tsx
│       ├── WatchView.tsx
│       ├── ReplView.tsx
│       ├── SessionsView.tsx
│       └── GroupsView.tsx
```

---

## json-render integration (health view)

### Catalog

```typescript
// src/components/health/catalog.ts
import { defineCatalog } from '@json-render/core';

export const healthCatalog = defineCatalog({
  StatusBadge: {
    description: 'Pass/warn/fail badge',
    props: { status: z.enum(['pass', 'warn', 'fail']), label: z.string() },
  },
  CheckTile: {
    description: 'Single health check result card',
    props: {
      name: z.string(),
      status: z.enum(['pass', 'warn', 'fail']),
      detail: z.string(),
      remediation: z.string().optional(),
    },
  },
  InstallationSection: {
    description: 'Group of checks for one arch installation',
    props: { arch: z.string(), source_dir: z.string() },
  },
  RemediationAlert: {
    description: 'Actionable fix suggestion',
    props: { message: z.string(), command: z.string().optional() },
  },
});
```

### Registry

```typescript
// src/components/health/registry.tsx
import { defineRegistry } from '@json-render/react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Alert, AlertDescription } from '@/components/ui/alert';

export const healthRegistry = defineRegistry(healthCatalog, {
  StatusBadge: ({ status, label }) => (
    <Badge variant={status === 'pass' ? 'default' : status === 'warn' ? 'secondary' : 'destructive'}>
      {label}
    </Badge>
  ),
  CheckTile: ({ name, status, detail, remediation }) => (
    <Card className={status === 'fail' ? 'border-destructive' : status === 'warn' ? 'border-yellow-400' : ''}>
      <CardHeader className="pb-1 flex-row items-center gap-2">
        <StatusIcon status={status} />
        <span className="font-mono text-sm">{name}</span>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        {detail}
        {remediation && <RemediationAlert message={remediation} />}
      </CardContent>
    </Card>
  ),
  // ...
});
```

### Server-side spec generation

`claw api` emits a render spec alongside raw check results when `Accept: application/json+render` is set (opt-in, backward compatible):

```go
// api/rest_health.go
func buildRenderSpec(results []checkResult) map[string]interface{} {
    elements := map[string]interface{}{}
    children := []string{}
    for _, r := range results {
        id := "check-" + r.Name
        el := map[string]interface{}{
            "type": "CheckTile",
            "props": map[string]interface{}{
                "name":   r.Name,
                "status": r.Status,
                "detail": r.Detail,
            },
        }
        if r.Remediation != "" {
            el["props"].(map[string]interface{})["remediation"] = r.Remediation
        }
        elements[id] = el
        children = append(children, id)
    }
    elements["root"] = map[string]interface{}{
        "type": "InstallationSection",
        "props": map[string]interface{}{"arch": results[0].Arch},
        "children": children,
    }
    return map[string]interface{}{"root": "root", "elements": elements}
}
```

This means: new check types, custom remediation widgets, driver-specific
presentation — all handled in Go, zero frontend changes.

---

## WebSocket connection model

```typescript
// src/lib/ws.ts
export function createWS(path: string, onMessage: (msg: unknown) => void): () => void {
  const { apiUrl, token } = useConnectionStore.getState();
  const url = new URL(path, apiUrl.replace('http', 'ws'));
  if (token) url.searchParams.set('token', token);

  const ws = new WebSocket(url);
  ws.onmessage = (e) => onMessage(JSON.parse(e.data));
  ws.onerror = () => { /* reconnect with backoff */ };

  return () => ws.close();
}
```

---

## View specs

### Health view
- Opens `WS /ws/health?interval=30`
- Renders json-render specs from each `health_complete` message
- Tiles update in place — no flash, status colours animate on change
- Overall pass/warn/fail count in top bar

### Agents view
- `GET /api/v1/ps` every 5s
- Table: arch, group, state (running/idle/stuck), age, is_main badge
- Click row → jump to Watch for that group

### Watch view
- Group picker (populated from `GET /api/v1/groups`)
- Opens `WS /ws/watch/:group` on selection
- Scrolling message feed, auto-scroll pinned to bottom unless user scrolls up
- Bot messages visually distinct from user messages

### REPL view
- Group picker + session picker (from `GET /api/v1/sessions`)
- Opens `WS /ws/agent/:group`
- Text input + submit; chunks stream in as they arrive
- Session ID shown in footer after first completion
- "New session" / "Resume" buttons

### Sessions view
- `GET /api/v1/sessions?arch=...&group=...`
- Searchable table: group, started, last active, message count, summary
- Click → opens REPL pre-loaded with that session ID

### Transport detection (`src/lib/jid.ts`)

```typescript
export type Transport = 'whatsapp' | 'telegram' | 'signal' | 'discord' | 'unknown';

export function transportFromJID(jid: string): Transport {
  if (jid.startsWith('tg:'))      return 'telegram';
  if (jid.startsWith('signal:'))  return 'signal';
  if (jid.startsWith('dc:'))      return 'discord';
  if (jid.endsWith('@g.us') || jid.endsWith('@s.whatsapp.net')) return 'whatsapp';
  return 'unknown';
}

// Color + icon map for TransportBadge
export const TRANSPORT_META: Record<Transport, { label: string; color: string; icon: string }> = {
  whatsapp:  { label: 'WhatsApp', color: 'bg-green-600',  icon: '💬' },
  telegram:  { label: 'Telegram', color: 'bg-blue-500',   icon: '✈️' },
  signal:    { label: 'Signal',   color: 'bg-indigo-600', icon: '🔒' },
  discord:   { label: 'Discord',  color: 'bg-purple-600', icon: '🎮' },
  unknown:   { label: 'Unknown',  color: 'bg-gray-400',   icon: '?' },
};
```

`<TransportBadge jid={jid} />` derives transport, looks up meta, renders a
colored pill. Used in Groups cards, Watch header, and Agents table.

---

### Groups view
- `GET /api/v1/groups`
- Cards: name, `<TransportBadge>`, arch badge, JID, trigger, is_main indicator
- Read-only — no edit (config lives in drivers)

### Watch view header
- Group name + `<TransportBadge>` + live/paused indicator
- If transport is non-WhatsApp and `watch-bot-messages` fix not yet shipped,
  show subtle `⚠ bot responses may not appear` tooltip on the badge

---

## Connection setup

First-time load shows `ConnectModal`:
- API URL (default `http://localhost:7474`)
- Bearer token (optional, stored in `localStorage`)
- "Connect" → stores in `useConnectionStore`, persists to localStorage

Top bar shows connection status dot (green/yellow/red).

---

## Distribution

**Embedded in `claw api serve --console`:**
```go
//go:embed console/dist
var consoleFiles embed.FS
```
Served at `/` when `--console` flag is set. `claw api serve --console` opens the browser automatically.

**Standalone (separate origin):**
`npm run build` → `dist/` — host anywhere. Set `VITE_API_URL` at build time or configure at runtime in ConnectModal.

---

## Delivery phases

### Phase 1 — Core (MVP)
- Project scaffold: Vite + React + TypeScript + shadcn/ui + Zustand
- ConnectModal + connection store
- Health view (json-render integration)
- Agents view (polling)
- `claw api serve --console` embedding

### Phase 2 — Live feeds
- Watch view (WebSocket) — with `<TransportBadge>` in header
- REPL view (WebSocket, streaming)
- Session continuity (resume session from REPL)

### Phase 2.5 — Diagnostics
- Log drawer in REPL view: `WS /ws/logs/:group` → `docker logs -f` stream
- Collapsible panel, auto-opens when agent is running
- Container-mode groups only (`arch: nanoclaw` with Docker). SDK/native-mode
  groups run in worker threads — log drawer shows "native mode" notice instead.
- Transport badge added to Agents table row

### Phase 3 — Full
- Sessions view (searchable history)
- Groups view
- Dark mode (Tailwind dark: classes, system preference)
- Keyboard shortcuts (focus REPL, switch views)
