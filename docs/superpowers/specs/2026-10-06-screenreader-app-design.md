# ScreenReader App — Design Spec

- **Date:** 2026-10-06
- **Status:** Pending user review
- **Repo:** `screen_reader`
- **Platform target:** macOS (local machine)
- **Supersedes (partially):** `2026-10-03-screenreader-mcp-design.md` Component 2 — the deferred Swift
  config window app, revived and extended here into the full host app. The `2026-10-03` spec remains
  authoritative for the Go engine (tools, coordinate model, config semantics).

## 1. Purpose

Refactor the project from "Go stdio MCP server + TCC granted to the terminal" into a **menu bar app**
the user opens, grants permissions to, and configures. While the app is open it hosts the MCP server
endpoint for **any local agent** (opencode or other MCP clients) to use.

Driving requirements (from `TODO.md`):

- The user flow is to open the app & adjust settings (what screen/window/rectangle to record).
- It shall be a Mac app (Swift) that lives in the menu bar and has a small app window.
- When open, the app hosts itself as an MCP server target to be configured and used in opencode;
  the app shows the instructions.
- The app itself requests the required access.
- The app then provides control to any agent via MCP.
- The MCP server shall be written in Go; the Go server and the Swift app need to communicate.

> Interpretation: "what screen/window/rectangle to **record**" means "to **capture**" — the project
> target for the existing capture/OCR/input tools. It does **not** introduce video recording.

## 2. Goals / Non-goals

**Goals**

- A menu bar app (Swift/AppKit) with a small settings window that owns onboarding: TCC permissions,
  target selection (window picker or drawn rectangle), server status.
- The app hosts the MCP server on a **loopback-only Streamable HTTP endpoint** while it is open, and
  displays a copy-paste-ready opencode config snippet in-app.
- TCC permissions (Screen Recording, Accessibility, Automation) are granted to **the app**, not the
  terminal; the app requests them.
- The existing Go engine (17 tools) is reused **verbatim** except for a new HTTP serving mode.
- Local-only access (127.0.0.1), protected by a static bearer token.

**Non-goals (v1)**

- No LAN/remote access (loopback binding enforced).
- No OAuth (static bearer token is sufficient for loopback).
- No WebSocket endpoint (not an MCP standard transport) and no legacy HTTP+SSE endpoint
  (deprecated transport; the SDK can add it later if an older client demands it).
- No multi-target orchestration, no recording/replay, no scheduling.
- No changes to the tool surface, coordinate model, or existing config field semantics.
- No CI.
- No software on the remote machine.

## 3. Architecture

```
opencode (or any MCP client)
   │  MCP over Streamable HTTP — loopback only
   ▼
http://127.0.0.1:18427/mcp
   │
┌──────────────────────────────────────────────┐
│ Go binary "screenreader" (existing engine)    │ ◀─ child process of the app
│ 17 tools: capture / ocr / input / windows     │
└──────────────────────────────────────────────┘
        ▲ spawn, /health poll, tools/call
┌──────────────────────────────────────────────┐
│ ScreenReader.app (Swift, menu bar, LSUIElement)│
│ • menu bar item + small settings window       │
│ • owns TCC permissions (Screen Recording,     │
│   Accessibility, Automation)                  │
│ • window picker + rectangle-drawing overlay   │
│ • writes config.json, shows opencode snippet  │
└──────────────────────────────────────────────┘
```

**Operating principles**

1. **The app is a host, not a proxy.** It never sits in the agent↔server data path — clients talk
   directly to the Go HTTP endpoint over loopback.
2. **The Go engine is reused verbatim.** All 17 tools, the coordinate model, and per-action config
   re-reads are untouched. Go-side additions are limited to §5.
3. **TCC lives in the app.** macOS attributes a child process's protected calls to the responsible
   parent app, so grants given to `ScreenReader.app` cover the Go child's capture/input/osascript
   (§7). Validated by "Test capture" as the first implementation task.
4. **The Go binary ships inside the app bundle** (`Contents/Resources/screenreader`), so "open the
   app" is the entire setup.
5. **Lifecycle:** app launch → spawns the server; app quit → server exits (the MCP endpoint
   disappears while the app is closed — intended); server crash → auto-restart with backoff, state
   shown in the menu bar.
6. **Config is the single source of truth.** The app writes `~/.config/screenreader/config.json`;
   the Go server re-reads it per action (existing mechanism) — changes apply live, no restart.
7. **The app has a small built-in MCP-over-HTTP client** (§4.4) powering "Test capture", the window
   picker (via the existing `list_windows` tool — no duplicated enumeration logic), and status.

## 4. Component 1 — `ScreenReader.app` (Swift/AppKit)

### 4.1 Stack

- **AppKit** (not SwiftUI — the flipped-coordinate full-screen overlay and multi-display windows are
  first-class in AppKit). Swift Package in `app/` plus a root `build.sh` that assembles and signs
  the `.app` bundle (no committed `.xcodeproj`; validated by spike — fast builds, `swift test` in
  seconds, same AppKit result).
- `LSUIElement` = true (menu bar only, no dock icon). App name `ScreenReader`,
  bundle ID `dev.chack.screenreader`.
- Ships the Go binary at `Contents/Resources/screenreader` (copied by `build.sh`, §11).

### 4.2 Menu bar item

- Template icon. Polls `GET /health` every ~2 s while the server is running.
- Disabled status rows:
  - `Server: running on 127.0.0.1:18427` / `Server: not running` / `Server: restarting…`
  - `Target: <app> — <title>` or `Target: rectangle (x, y, w, h)` or `Target: not set`
  - Permission rows: `Screen Recording ✓/✗`, `Accessibility ✓/✗`, `Automation ✓/✗`
- Actions:
  - **Open ScreenReader…** — opens the settings window.
  - **Restart server** — kills and respawns the child.
  - **Test capture…** — calls `capture` via the built-in MCP client on the current target;
    **fails if the image is all-black (every pixel ≤ 8)**; result + likely fix shown in an alert.
    This is also the TCC-attribution canary (§7) and the end-to-end connectivity check.
  - **Quit ScreenReader** — terminates the child, then the app.

### 4.3 Settings window (~460×560)

**Section 1 — Permissions.** Three rows, each with a live status and a **Request** button:

| Permission | Status source | Request trigger |
| --- | --- | --- |
| Screen Recording | `CGPreflightScreenCaptureAccess()` | `CGRequestScreenCaptureAccess()` |
| Accessibility | `AXIsProcessTrusted()` | `AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt: true])` |
| Automation (System Events) | probe: `osascript -e 'tell application "System Events" to return 1'` | same probe (triggers the TCC prompt on first control) |

- On first launch with anything missing: an onboarding **Grant access** button.
- Footnote: "If a status looks stale after granting, quit and relaunch the app."

**Section 2 — Target.** Segmented control **App window | Rectangle**, mirroring config `mode`.

- *App window*: window picker (`NSPopUpButton`) populated from the server's `list_windows` via the
  built-in client — rows show `app — title`; a **Refresh** button; selecting a row writes
  `app`/`title` to config (`mode: "app"`). When the server is not running: a hint + **Start server**.
- *Rectangle*: **Select rectangle…** button → full-screen transparent overlay, one window per
  display, dragged rect captured in **screen-absolute, top-left-origin** coordinates via a
  **flipped view** (`isFlipped = true`) — the approach from the 2026-10-03 spec, so no Y-flip bug.
  Escape cancels; the resulting `x, y, w, h` is displayed and written to config (`mode: "rect"`).

**Section 3 — Server.** Status (running/stopped + address), and **Copy opencode config** →
clipboard. The snippet is also displayed read-only in the window (satisfies "add instructions in
app"):

```json
{ "mcp": { "servers": { "screenreader": {
    "type": "remote",
    "url": "http://127.0.0.1:18427/mcp",
    "oauth": false,
    "headers": { "Authorization": "Bearer <token>" } } } } }
```

(`<token>` and `<addr>` are filled in from the live config.)

### 4.4 Built-in MCP client

- `initialize` handshake (protocol-version negotiation) + `notifications/initialized` +
  `tools/call` **only**. Tools used: `list_windows`, `window_info`, `capture`, `doctor`.
- JSON-RPC 2.0 over `URLSession` to `http://<addr>/mcp`; carries the `Mcp-Session-Id` header from
  the initialize response; sends `Accept: application/json, text/event-stream` and parses either a
  single JSON response or a short SSE event stream.
- No prompts, resources, or subscriptions in v1.

### 4.5 ConfigStore

- Reads/writes `~/.config/screenreader/config.json`.
- Mutates **only** `app`, `title`, `mode`, `rect`, and the `http` section; **preserves all other
  keys** (`version`, `ocr`, `capture`, `input`).
- Validates existing JSON before writing; **refuses to write over an unparseable file** and surfaces
  the error in the UI.
- Atomic write (temp file + rename).

### 4.6 Code signing & TCC identity

- Create a **one-time self-signed code-signing certificate** in the login keychain (no Apple account
  required); `build.sh` signs the app bundle with it.
- Rationale: TCC identifies apps by code-signing identity. An ad-hoc-signed app is keyed by cdhash —
  every rebuild changes it and **invalidates all grants**. A stable self-signed identity keeps
  grants across rebuilds.
- If the identity is ever changed, grants must be re-granted once (documented in README).

## 5. Component 2 — Go server changes

The diff is deliberately small: one flag, one config section, a mux, one middleware.

### 5.1 New flag `--http`

- Enables HTTP mode. Default (flag absent): stdio — **byte-for-byte unchanged** from today, so the
  current opencode setup and all existing behavior/tests keep working.
- One process, one transport.

### 5.2 New config section

```json
"http": { "addr": "127.0.0.1:18427", "token": "<32-byte hex>" }
```

- Defaults when absent: `addr = "127.0.0.1:18427"`, `token = ""`.
- The app generates the token (32 random bytes, hex) on first run / when missing and writes the
  section. A manual user may edit it too.

### 5.3 Bind validation

- `http.addr` host must be loopback (`127.0.0.1` / `::1`; `localhost` accepted and resolved).
  Any other host → refuse to start with a clear error (MCP spec local-security guidance).
- Port already in use → clear error on stderr.

### 5.4 Transport wiring

- `main.go` constructs the single `*mcp.Server` (all 17 tools registered once) shared by both modes.
- HTTP mode mounts, via an `http.ServeMux`:
  - `GET /health` — **unauthenticated**, returns
    `{"ok": true, "name": "screenreader", "version": "<v>", "addr": "127.0.0.1:18427"}`. No secrets.
  - `/mcp` — `mcp.NewStreamableHTTPHandler` (SDK v1.8.0) serving the shared server, wrapped with the
    SDK `auth.RequireBearerToken` middleware when a token is configured. The handler is configured
    with `StreamableHTTPOptions{JSONResponse: true}` — responses are plain JSON (the in-app client
    still tolerates SSE bodies).
- Token is **re-read from config per request** (same live-re-read pattern as actions) — rotation by
  editing config takes effect immediately.
- No token configured → HTTP mode still runs unauthenticated, but logs one loud warning to stderr.

### 5.5 Backward compatibility

stdio mode, the tool surface, config field semantics, and the coordinate model are all unchanged.
README gains an "HTTP mode / app" section (§12).

## 6. Connectivity (verified)

- The MCP spec defines exactly **two standard transports: stdio and Streamable HTTP**. WebSocket is
  not part of the spec (deliberately excluded); HTTP+SSE (2024-11-05) is deprecated. The Go SDK can
  serve the legacy SSE endpoint (`NewSSEHTTPHandler`) later if an older client demands it — out of
  scope for v1.
- opencode V2 supports `type: "remote"` (Streamable HTTP) with `oauth: false` and `headers` —
  verified against the V2 docs. The in-app snippet (§4.3) is the exact server entry to merge into
  `opencode.json`'s `mcp.servers`.
- **Loopback only:** only local processes can reach the endpoint; the bearer token additionally
  protects against other local processes.

## 7. Permissions & TCC identity

- The app requests all three permissions natively (§4.3).
- **Attribution model:** macOS TCC attributes a child process's protected calls to the *responsible*
  parent app. The Go binary is spawned by the app, so its screen capture, synthetic input, and
  AppleScript run under **the app's grants** — this is why helper processes inherit their parent
  app's permissions on macOS.
- **Risk & gate:** attribution is the single external assumption of this design. **Plan task 14 is
  the Test-capture canary** (§4.2): app grants → Go child → non-black image — and plan task 17
  re-runs it as the final integration gate. If it ever fails, stop and escalate to Option B
  (Swift-native low-level layer, Go reduced to the protocol layer) — a fallback decision, not
  part of v1.
- TCC grants are evaluated by the OS at request time; after granting, the app may need a relaunch to
  refresh cached status (noted in the UI).

## 8. Data flow & lifecycle

- **Agent → Go:** direct over loopback HTTP. The app is never in the data path.
- **Swift → Go:** spawn (`screenreader --http`), liveness watch, `SIGTERM` on quit (3 s grace →
  `SIGKILL`).
- **Go → Swift:** `/health` polling (status), child exit code, stderr tail (crash display).
- **Config:** app writes, Go re-reads per action ⇒ live updates without restart.
- **Crash handling:** unexpected child exit → respawn with backoff 1 s, 2 s, 4 s, … capped at 30 s.
  Three rapid crashes → stop retrying; the Server section shows a stderr excerpt and a **Restart
  server** action.

## 9. Error handling

| Situation | Behavior |
| --- | --- |
| Port in use | Go exits with a clear error; app shows `Port 18427 busy — change http.addr in config`; menu bar: server stopped |
| Server crash | Auto-restart with backoff (§8); after 3 rapid crashes: stop + stderr excerpt in Server section |
| App quit | Child terminated; MCP endpoint gone until relaunch (intended) |
| Missing/misattributed permission | Test capture fails (black image) with "grant <permission> to **ScreenReader** — check System Settings → Privacy & Security shows the app, not your terminal"; `doctor` gives agents the same view |
| Config unparseable | Go: existing behavior; app: refuses to write, surfaces the error |
| Client churn (opencode reconnects) | No server-side action needed — Streamable HTTP sessions are per-connection |
| No token set | Server runs, one loud stderr warning; app always generates a token, so this is a manual-use case |

## 10. Testing strategy

**Automated (Go, `go test ./...` with CGO):**

- All existing pure-logic tests (geom, config, keycombo, window parsing) stay green.
- New: Streamable handler over `httptest` (`initialize` + `tools/list`), `/health` via the mux,
  loopback bind validation (reject non-loopback), token middleware (401 absent/wrong, 200 correct).

**Automated (Swift test target, `ScreenReaderTests`):**

- ConfigStore merge/preserve semantics (all other keys survive).
- Rect normalization (clamping to display bounds, top-left-origin invariants).

**Manual integration checklist** (dev Mac, real permissions; included in the implementation plan):

1. `build.sh` → open app (first run).
2. Grant all three permissions when prompted → all app statuses green.
3. **Test capture → non-black** (TCC-attribution gate; everything else depends on this).
4. Window picker lists windows; select the Remote Desktop window → config updated; `window_info` resolves.
5. Rectangle mode: draw on the overlay → capture of that rect matches the saved `x, y, w, h`.
6. Copy opencode config → paste into `opencode.json` → `/mcps` shows connected → an agent runs
   `capture` / `ocr` / `click` against the Remote Desktop window successfully.
7. Quit app → endpoint gone; relaunch → endpoint back; menu bar status correct throughout.
8. Rebuild the app → grants persist (stable self-signed identity).

## 11. Project layout

```
screen_reader/
  server/                      # Go engine (existing)
    main.go                    # + --http flag; mux /health + /mcp
    internal/httpmode/         # NEW: loopback validation, /health, token middleware
    internal/config/           # + http section (addr, token)
    internal/…                 # otherwise unchanged
  app/                         # NEW — Swift Package (SPM)
    Package.swift
    Info.plist                 # LSUIElement, bundle id — build.sh embeds it in the bundle
    Sources/ScreenReaderKit/   # pure logic: ConfigStore, RectMath, Snippet, WindowLine,
                               #   Backoff, CaptureCheck, MCPClient, Permissions
    Sources/ScreenReader/      # AppKit app: main, AppDelegate, ServerProcess,
                               #   SettingsWindow, RectOverlay
    Tests/ScreenReaderKitTests/
  build.sh                     # NEW: go build → swift build → assemble .app → codesign
  docs/superpowers/specs/      # + this spec
  README.md                    # + HTTP mode / app section
```

No CI (consistent with the repo today). Gate changes on `go vet` + `go test` and the Swift Package
build + `swift test`.

## 12. Documentation updates

- **README:** new "HTTP mode / app" section — build via `build.sh`, open the app, grant permissions,
  copy the snippet; manual `--http` usage; token rotation; the self-signed identity setup (one-time)
  and the "change identity → re-grant once" note.
- **AGENTS.md:** the note marking the Swift app as an unapproved plan is updated to point at this
  spec once it is approved (plan task).

## 13. Risks & open items

| Risk | Mitigation |
| --- | --- |
| TCC attribution of the Go child process (§7) | Test-capture canary as plan task 1; Option B as documented escalation path |
| TCC re-prompt after signing-identity change | Self-signed identity created once; documented |
| cgo build friction (robotgo + tmc/apple need Xcode CLT) | Unchanged from the 2026-10-03 spec; documented |
| tmc/apple maturity | Unchanged; isolated in `internal/ocr` |
| Real-cursor side effects | Unchanged: `dry_run`, pinned targets, README safety section |

## 14. Out of scope (v1)

- LAN/remote access, OAuth, WebSocket, legacy SSE endpoint.
- Multi-target orchestration, recording/replay, scheduling.
- SwiftUI, app distribution (outside the user's machine), CI.
- Software on the remote machine.
