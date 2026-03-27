# Claw Operator Toolchain — Roadmap

The goal is a complete set of tools for operators running claw agent
infrastructure at scale: one or many installations, one or many architectures,
one or many machines.

---

## Current tools

| Tool | Status | Purpose |
|------|--------|---------|
| `claw` | v0.1 | Unified CLI — run agents, watch conversations, inspect instances |
| `molt` | v0.1 | Migration — export/import groups, memory, skills between architectures |

---

## Planned tools

### `claw health`

Diagnostic health checks across installations.

- Credential validity (API key / OAuth token expiry)
- Container / runtime reachability
- DB integrity check
- Stuck/zombie session detection
- Disk space on group dirs and session dirs
- Traffic-light output: green / yellow / red per installation
- `--watch` mode for continuous monitoring
- Emits structured JSON for piping into alerting

**Why first:** everything else depends on knowing your installations are healthy.
This pays for itself immediately.

---

### `claw upgrade`

Version management for claw architecture installs.

- Checks running version against latest release (per arch)
- Pulls updates, rebuilds containers
- `--all` flag upgrades every installation the driver can reach
- Dry-run support
- Pre/post upgrade health check (uses `claw health`)
- Rollback: saves previous image before upgrading

---

### `claw skill`

Unified skill management across installations and groups.

- `claw skill list [-g <group>]` — list installed skills
- `claw skill install <url> [-g <group>]` — install from URL or path
- `claw skill remove <name>` — uninstall
- `claw skill sync` — push a skill to all groups in an installation
- `claw skill diff` — compare skill sets between groups or installations

---

### `claw secrets`

Credential management across installations.

- `claw secrets list` — show which keys are set (values redacted)
- `claw secrets rotate --key ANTHROPIC_API_KEY` — push new value to all `.env`
  files and hot-reload running agents
- `claw secrets audit` — flag expired or soon-to-expire tokens
- Reads from system keychain or environment; writes to `.env` via driver

---

### `molt sync`

Continuous incremental backup (extends `molt`).

- Daemon mode: watches for changes, exports deltas on a schedule
- Configurable destinations: local dir, S3, SFTP, rsync target
- `molt sync start --interval 6h --dest s3://my-backups/`
- `molt sync status` — last run, next run, diff since last export
- `molt sync restore <timestamp>` — point-in-time restore
- The disaster recovery story for production claw deployments

---

### `claw bench`

Regression testing and evaluation across installations.

- Runs a standard prompt suite against a group or installation
- Compares output between versions, configs, or architectures
- Useful before/after upgrades or CLAUDE.md changes
- `--baseline <session>` — compare against a saved reference run
- Output: pass/fail per prompt, diff on failures

---

## Driver protocol extensions

As the toolchain grows, the driver protocol will need new request types:

| Request type | Purpose | Used by |
|---|---|---|
| `health_request` | Installation diagnostics | `claw health` |
| `upgrade_request` | Pull + rebuild | `claw upgrade` |
| `skill_request` | Skill CRUD | `claw skill` |
| `secrets_request` | Credential management | `claw secrets` |

Each is additive — drivers can return `unsupported` for request types they
don't implement yet.

---

## Rough sequence

1. `claw health` — immediate operational value, informs everything else
2. `molt sync` — production safety net before pushing upgrades
3. `claw secrets` — operational pain point once you have 3+ installations
4. `claw upgrade` — depends on health + secrets being solid first
5. `claw skill` — nice to have, lower urgency
6. `claw bench` — longer tail, needs baseline data to be useful
