# Offline Mode (keep-alive stop) — Design Spec

- **Date:** 2026-11-19
- **Status:** Pending user review
- **Repo:** `screen_reader`
- **Platform target:** macOS 13+ (the app floor from `app/Package.swift`)
- **Extends:** `2026-10-03-screenreader-mcp-design.md` (Go engine: tools, config, access guard) and
  `2026-10-06-screenreader-app-design.md` (app: menu, ServerProcess, ConfigStore, hotkey, auto-stop timer).

## 1. Purpose

Today every "stop" (menu item, F9 hotkey, 4 h auto-stop, settings button) SIGTERMs the Go child, so
the MCP client (opencode) marks the whole server as a **failed connection** — noisy, and it makes
"stopped" and "gone" indistinguishable to the agent.

Make "stop" a **pause**: the Go process keeps running for as long as the app is open, the MCP
connection stays healthy, and while paused every tool call is refused with a clean status:
**"offline"**. Unpause resumes instantly. Config-driven, hot-applied — the same live-switch
machinery `access` already ships.

Driving decisions (settled during brainstorming):

- **Stop = pause everywhere**: menu, F9 hotkey, 4 h timer, and the settings-window button all flip
  the offline flag; **none of them kills the process anymore**.
- **Everything returns "offline"**: all 15 tools, including `doctor` and `wait`. Uniform contract;
  nothing can transact while paused.
- **App launch resets to online**: the pause is runtime state, not a boot policy — when the app
  starts the server at launch, it clears the flag. (A crash-respawn of the child while the app
  stays up is *not* an app launch: the child comes back still paused.)

## 2. Lifecycle invariant (the core contract)

**The MCP server process runs if and only if the Mac app runs.**

| Event | Server process | Offline flag |
|---|---|---|
| App launch | spawned (fresh, stale-port sweep first) | cleared by the app → online |
| Stop (menu / F9 / 4 h timer / settings) | **untouched, stays up** | set → every tool call answers "offline" |
| Start (menu / settings) | untouched if up; `start()` if crashed/stopped | cleared → online |
| Child crash | app respawns with backoff (unchanged) | preserved — respawn keeps it paused |
| App quit / SIGTERM | killed (`applicationWillTerminate` / existing signal handler) | — |

After the app quits the connection drops — inherent to the app being closed; this spec guarantees
"while the app is open" only. No UI path exists anymore that kills the child while the app stays
open; the only kills are quit, crash bookkeeping, and the launch-time `killStaleListener` sweep
(which *enforces* the invariant against orphans).

## 3. Goals / non-goals

**Goals**

- MCP clients keep a live, healthy connection for the whole app session; a paused server answers
  every tool call with an explicit `offline` status instead of a dead socket or "tool not found".
- Live switch both ways without restarting the server: works for HTTP (per-request rebuild) **and**
  long-lived stdio sessions (per-call guard), and for bare `screenreader` users editing config by hand.
- F9 keeps its meaning ("make it stop doing things") and the 4 h cap still bounds real work — both
  stop paths become pauses.
- Camera hardware released when paused (explicit close on the next guarded call; the existing 30 s
  idle teardown is the outer bound).
- Fail **closed** for the flag itself: an unreadable config rejects tool calls (a corrupt config
  must never silently re-enable real clicks after the user pressed Stop).

**Non-goals**

- No keeping the server alive after app quit, no LaunchAgent/daemon mode.
- No scheduled auto-*resume*, no per-tool offline policies, no HTTP status API beyond the existing
  `/health` (200 = process healthy; unchanged).
- No change to `access` semantics: `access` scopes the tool *surface*; `offline` is the kill switch.
  The two compose; offline wins.
- No config file watching/polling in Go — the flag is observed on the next call, like every other
  config key.

## 4. Architecture

```
┌────────────────────────────────────────────────────────────┐
│ Screen Reader MCP.app (Swift)                               │
│  menu / F9 / 4h timer / settings button                     │
│        └─ AppDelegate.stop()/.start()  (single choke point) │
│             ├─ ConfigStore.applyOffline(true|false) → config│
│             └─ start also: server.start() when process down │
│  app launch: offline=false (reset) → server.start()         │
│  app quit:   kill child (existing SIGTERM paths)            │
└────────────────────────────────────────────────────────────┘
config.json  ── re-read per call (existing) ──▶  ┌───────────┐
  "offline": true|false                          │ Go server │  /health → 200 (unchanged)
                                                 │  always   │  tools/list (unchanged while paused)
                                                 │  running  │  tool call → offlineGuard → all tools
                                                 └───────────┘        ├ offline=true  → isError "offline…"
                                                                      │                 + camera.Close()
                                                                      └ offline=false → accessGuard → handler
```

### 4.1 Go engine (`server/`)

- `internal/config`: `Offline bool` (`json:"offline"`, default `false`). Carried in the struct so
  `set_target`'s Save never erases the app's key (the `hotkey`/`camera` precedent), and read live by
  the guard on every call.
- `internal/tools/access.go` (next to `accessGuard`): `offlineGuard()` runs **before** the access
  check inside `add()`, so all current and future tools are covered by construction. Registered
  tool list never changes while paused — clients keep a stable surface and get an explicit error:

  - `offline`: `offline — server stopped from the Screen Reader app (start it from the menu bar,
    or set "offline": false in config.json)`
  - config unreadable while the guard runs: refused — `server refused the call: config.json is
    unreadable (<err>); fix or remove it, then start the server from the app` (fail closed;
    deliberate divergence from `accessGuard`'s fail-open, which only gates *which* tools run).
- `internal/camera`: new exported `Close()` — mutex-guarded, idempotent, cancels the idle timer and
  releases the session; no-op when nothing is open. `offlineGuard` calls it on every rejection, so
  an agent hammering tools can never reopen or hold the camera while paused; the 30 s idle TTL
  stays the outer bound for the no-calls case. (Keeps the camera spec's "stop always releases the
  camera" promise under the new pause semantics.)
- `doctor`'s config line gains `offline=%v` for when it runs again after resume.

### 4.2 App (`app/`)

- `ScreenReaderKit/ConfigStore`: `offline(from:) -> Bool` (missing → `false`) and
  `applyOffline(_:to:)` (writes the bool explicitly, preserves every other key — the `access`
  pattern). Mutated-keys list grows to include `offline`.
- `AppDelegate` becomes the single stop/start choke point:
  - **stop** = load → `applyOffline(true)` → save → update menu; also cancels a pending 4 h
    deadline (nothing left to auto-stop). Config write failure: same alert + leave-config-alone
    contract as the access toggle; the server stays online rather than half-stopped.
  - **start** = load → `applyOffline(false)` → save; `server.start()` if `server.state` is
    down (`stopped`/`crashed`); schedules a fresh 4 h window (matching today's "explicit start
    opens a fresh window").
  - F9 (`HotkeyCenter` callback) and `tickMenu()`'s 4 h fire call **stop**; they no longer call
    `server.stop()`.
  - Launch: before `server.start()` in `applicationDidFinishLaunching`, clear the flag when set
    (reset-to-online). `applicationWillTerminate` keeps the real `server.stop()` (kill).
- `SettingsWindowController`: its "Stop server"/"Start server" button no longer calls
  `server.toggle()`; it invokes an `onServerAction` callback wired to the same AppDelegate choke
  point (same injection pattern as `hotkeyChanged`). Its refresh timer relabels from
  `server.state.isUp` **and** the offline flag.
- `ServerProcess` is unchanged — it becomes purely process lifecycle (launch, crash-backoff,
  quit-kill, stale sweep). Its `toggle()`/`stop()` keep their exact behavior for internal callers.

### 4.3 UI states (menu & settings)

| Process | Flag | Status line | Action item | Dot badge | 4 h item |
|---|---|---|---|---|---|
| up | online | `running on <addr>` | `Server: stop (⌥F9)` | on | visible |
| up | offline | `offline — stopped (still listening on <addr>)` | `Server: start` | **off** | hidden |
| starting/restarting | — | `starting…` | `Server: stop (…)` | on | per today |
| crashed | — | `CRASHED — check the log` | `Server: start` | off | hidden |
| stopped | — | `not running` | `Server: start` | off | hidden |

The dot badge now means "can do real work" (up **and** online). Hidden/shown rows keep the
fixed-structure no-teardown rule (`autoStopItem` precedent). The hotkey glyph stays on the stop
item only while the action it performs is "stop".

## 5. Error handling & edge cases

- **Config write fails during stop/start** → alert, config untouched, state unchanged (contract
  shared with `toggleAccess`).
- **Config corrupt while paused** → guard fails closed; the rejection message says the config is
  unreadable (the app's own writes keep it valid; corruption is hand-edit or disk trouble).
- **Paused child crashes** → backoff respawn (unchanged), comes back paused; menu shows
  `CRASHED — check the log` only after the 3 retries, as today.
- **`access` toggle while paused** → still just writes `access`; offline overrides any mode until
  resumed.
- **Bare stdio user** → same flag, same guard; the error text names config.json as the escape.
- **Old behavior removed**: the menu/hotkey/timer no longer free the port — intentional; nothing
  else needs the port while the app is open.

## 6. Testing

- **Go, cgo-free loop** (`internal/config`, `internal/tools` — tools tests need the existing
  cgo build tag discipline):
  - `Offline` default (`false`), load, round-trip — mirrors the `Access` cases.
  - `offlineGuard`: offline rejects **every** registered tool name; unreadable config rejects;
    online/absent passes through to `accessGuard` unchanged.
  - Session-registered-while-online, paused mid-session → rejection on next call
    (mirror `TestAccessGuard_blocksStaleSession`).
  - Tool surface (tools/list) is identical online vs offline — registration never filters offline.
- **Swift** (`swift test`): `ConfigStore.offline/applyOffline` get/set/preserve — mirrors `access`.
- **Live verification** (granted app + opencode, manual): stop from menu → opencode stays
  connected, any tool answers "offline"; F9 and the timer behave identically; start resumes with no
  reconnect; camera light off ≤ 30 s after stop with target=camera; app quit → connection drops.
- Gate: `CGO_ENABLED=1 go vet ./...` + `go test ./...` from `server/`, `swift build && swift test`
  from `app/`, `./build.sh` for the live pass.

## 7. Docs & version

- `AGENTS.md`: add `offline` to the app-mutated keys; reword the stop-hotkey bullet (F9 **pauses**
  the agent — config `offline` — and no longer kills the server; camera release via guard + idle
  teardown); note the lifecycle invariant (§2).
- `docs/reference.md`: update stop/hotkey/auto-stop wording.
- App bundle version → **1.4.0** (feature addition).
