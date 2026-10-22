# ScreenReader MCP — Design Spec

> **Superseded 2026-10-20 — OCR removed.** The `ocr`, `click_text`, and `wait_for_text` tools, the `internal/ocr` package, the `ocr` config section, and the Apple Vision (`tmc/apple`) dependency no longer exist. The tool surface is 15 tools and the model reads the captured PNG with its own vision (`capture.max_width` defaults to 0 = native resolution). OCR passages below are kept as a historical record. See `README.md` for current behavior.

- **Date:** 2026-10-03
- **Status:** Approved for implementation planning
- **Repo:** `screen_reader`
- **Platform target:** macOS (local machine running opencode)

## 1. Purpose

Let an opencode agent **see** and **drive** a Remote Desktop window that is running on the
user's Mac, so the agent can read files and do research on a remote machine where opencode
itself cannot be installed.

The system treats the Remote Desktop **window** as a controllable "virtual screen":
it captures the window's rendered pixels for the model to read, and synthesizes real
mouse/keyboard events at the window's screen location, which the Remote Desktop client
forwards to the remote machine.

**Protocol-agnostic by design.** Everything operates at the macOS OS/window level (window
bounds, screen-region capture, synthetic input). It works identically for RDP, VNC,
Chrome Remote Desktop, Parsec, TeamViewer, etc. Nothing is installed on the remote machine.

## 2. Goals / Non-goals

**Goals**
- Capture the target window (or a pinned rectangle) and return it as an image for a
  vision-capable model.
- Extract exact text via Apple Vision OCR (with bounding boxes).
- Provide mouse + keyboard control (click, double/right click, move, scroll, drag, type,
  paste, key combos) at screen-absolute coordinates.
- A simple setup app to choose the target app name and/or draw a target rectangle on screen.
- A single-binary Go MCP server; a clear, safe, self-diagnosing tool surface.

**Non-goals (v1)**
- No software on the remote machine.
- No multi-target / multi-window automation orchestration.
- No recording/replay, no scheduled actions.
- Not a general RPA framework.

## 3. Architecture

Two components share one config file.

```
┌─────────────────────────────┐        ┌──────────────────────────────┐
│  screenreader-config        │ writes │  ~/.config/screenreader/     │
│  (Swift/AppKit window app)  │───────▶│  config.json                 │
│  app name + rect selection  │        └──────────────┬───────────────┘
└─────────────────────────────┘                       │ read (per action)
                                                      ▼
   opencode  ──stdio──▶  ┌────────────────────────────────────────────┐
   (vision model)        │  screenreader  (Go, stdio MCP server)       │
                         │  window resolve → capture / OCR / input     │
                         └────────────────────────────────────────────┘
                                       │ macOS: Quartz/CoreGraphics/Vision/AppKit
                                       ▼
                          Remote Desktop window (on the Mac's screen)
```

- **`screenreader`** — Go stdio MCP server. The runtime. Launched by opencode.
- **`screenreader-config`** — small Swift/AppKit **window** app (no menu bar, no dock
  clutter). Setup only: pick app name and/or draw a target rectangle, then save.

**Single source of truth:** `~/.config/screenreader/config.json`. Written by the config
app and by the `set_target` tool. Read fresh on every capture/action, so edits apply
live without restarting the server.

## 4. Component 1 — `screenreader` (Go MCP server)

### 4.1 Stack & dependencies

| Concern | Library | Notes |
|---|---|---|
| MCP server (stdio) | `github.com/modelcontextprotocol/go-sdk` (Tier 1, v1.8) | `mcp.NewServer`, generic `AddTool`, `mcp.StdioTransport` |
| Mouse, keyboard, region capture, window mgmt, clipboard | `github.com/go-vgo/robotgo` (**Cgo backend**) | `Move/Click/Scroll/Drag/MouseDown/MouseUp`, `TypeStr/KeyTap`, `CaptureScreen(x,y,w,h)` (region), `GetBounds(pid)→x,y,w,h`, `ActiveName`, `GetTitle`, `SetActive`, `SetClipboard` |
| OCR (Apple Vision) | `github.com/tmc/apple` (`vision` package) | cgo bindings to the **system Vision framework** — in-binary, no external OCR install. Text + confidence + `boundingBox`; configurable languages + recognition level |

**Build:** `CGO_ENABLED=1 go build -o screenreader .` → one Mach-O executable linking only
macOS system frameworks (Quartz, CoreGraphics, Vision, AppKit). No runtime, no venv, no
Tesseract install.

**Requirement:** Xcode Command Line Tools (`xcode-select --install`) — needed by cgo for
robotgo and tmc/apple.

**Escape hatches (not the default):** if a native path misbehaves on a given machine,
`osascript` / `screencapture` / `cliclick` via `exec` can substitute for bounds/capture/
input, and `gosseract` (Tesseract) can substitute for OCR. These are documented fallbacks,
not the primary path.

### 4.2 Window targeting

- **Default target** comes from config: `app` (process name) + optional `title` (regex),
  or a **pinned `rect {x,y,w,h}`** (screen-absolute).
- Resolution:
  - `mode: "app"` → find the process by name (`robotgo` `ActiveName`/`GetTitle`), then
    `robotgo.GetBounds(pid)` → `{x, y, w, h}`.
  - `mode: "rect"` → use the fixed `rect` as-is.
- **Re-resolve bounds on every action** (for `app` mode) so the target survives the user
  moving/resizing the window. Pinned `rect` is fixed by definition.
- Per-call overrides (`app`, `title`, `rect`) on `capture`/`ocr`/input tools take precedence
  over config for that call. When no override is given, the config target is used.
- **Enumeration / title-based activation:** `list_windows` and title-matched `focus_window`
  use **AppleScript via `osascript`** (`tell application "System Events" … get {name,
  position, size, title} of every window of every process whose visible is true`), because
  robotgo's macOS window enumeration and title-based activation are limited. robotgo is
  used for the fast paths (activate-by-name, `GetBounds`).

### 4.3 Coordinate model

- **Screen-absolute is the default** for all input tools. Coordinates are in the global
  Quartz/window-server space: origin **top-left of the main display, Y down**. This matches
  robotgo's capture + mouse space and `GetBounds` output.
- `capture` returns metadata so the model can convert image→screen:
  - `bounds {x, y, w, h}` — the captured region, screen-absolute.
  - `image {width, height}` — the returned PNG's pixel size.
  - Mapping: `screenX = bounds.x + (px / image.width) * bounds.w` (same for Y). Proportional
    mapping is **DPI/Retina-safe** (the real image pixel size is used, not the point bounds)
    and robust to title bars / aspect-ratio letterboxing.
- `ocr` returns bounding boxes **already in screen-absolute pixel coordinates** (converted
  from Vision's normalized bottom-left space), so they are directly usable as click targets.
- Optional per-call `coords: "screen" | "image"` (default `"screen"`) on `click`/`move`/
  `drag`: when `"image"`, `x,y` are relative to the current target's captured image and the
  server maps them to screen. This is the escape hatch for models that prefer image space.

### 4.4 Tools

All tools return a result the model can read; failures return a clear, actionable error
(`isError`), including the likely fix (e.g., "window not found — call `list_windows`").

| Tool | Purpose | Key inputs | Returns |
|---|---|---|---|
| `set_target` | Set + persist the default target | `app?`, `title?`, `rect?{x,y,w,h}`, `mode?`("app"∣"rect") | resolved target + bounds |
| `focus_window` | Activate/raise the target window | `app?`, `title?` | resolved bounds |
| `window_info` | Inspect the resolved target | (optional `app?`,`title?`) | `{app, title, mode, bounds{x,y,w,h}}` |
| `list_windows` | List visible windows to pick a target | — | `[{app, title, bounds{x,y,w,h}}]` |
| `capture` | Screenshot the target (or region) | `app?`,`title?`,`rect?`,`max_width?` | image (base64 PNG) + `{bounds, image{w,h}}` |
| `ocr` | Apple Vision OCR of the target (or region) | `app?`,`title?`,`rect?`,`languages?`,`level?`("fast"∣"accurate"),`include_boxes?` | text + `[{text, confidence, box{x,y,w,h}(screen-absolute)}]` |
| `click` | Click (left/right/middle, single/double) | `x`,`y`,`button?`,`clicks?`(1∣2),`coords?`,`dry_run?` | executed + computed absolute `{x,y}` |
| `move` | Move cursor | `x`,`y`,`coords?` | executed |
| `scroll` | Scroll | `dx`,`dy`,`x?`,`y?`,`coords?` | executed |
| `drag` | Press, move, release | `x1`,`y1`,`x2`,`y2`,`button?`,`coords?`,`dry_run?` | executed |
| `click_text` | OCR-locate text, click its center | `text`,`match?`("exact"∣"contains"∣"regex"),`occurrence?`,`app?`,`title?`,`rect?` | clicked `{x,y}` + matched text/box |
| `type` | Type via keystrokes | `text` | executed |
| `paste` | Clipboard + ⌘V (reliable for long text/commands) | `text` | executed |
| `key` | Press a key/shortcut | `combo` (e.g. `return`, `tab`, `cmd+v`) | executed |
| `wait` | Sleep | `ms` | executed |
| `wait_for_text` | Poll OCR until text appears | `text`,`timeout_ms`(def 10000),`interval_ms?`(def 500),`match?`,`app?`,`title?`,`rect?` | `found`, `elapsed_ms`, box (if found) |
| `doctor` | Self-check: permissions, target, screen | — | permission status, resolved target, screen size, issues |

`capture` and `ocr` operate on the **full-resolution** region for accuracy; `max_width`
(downscale) on `capture` is only for token control of the returned image.

### 4.5 OCR details

- Write the captured region PNG to a temp file.
- `tmc/apple`: `foundation.NewURLFileURLWithPath` → `vision.NewImageRequestHandlerWithURLOptions`
  → `vision.NewVNRecognizeTextRequest` (set `SetRecognitionLanguages`, `SetRecognitionLevel`)
  → `handler.PerformRequestsError([...])` → `request.Results()`.
- Each observation: top candidate `String()` + `Confidence()`; `boundingBox` is normalized,
  **bottom-left origin**. Conversion (handled in `internal/geom`, unit-tested):
  - to image pixels (top-left): `ix = bx*W`, `iy = (1 - by - bh)*H`, `iw = bw*W`, `ih = bh*H`
  - to screen-absolute: `sx = bounds.x + (ix / W) * bounds.w`, etc.
- OCR always runs on the full-resolution capture (not the downscaled preview).

### 4.6 Input details

- `type` → `robotgo.TypeStr(text)` (keystroke synthesis; fine for short text, can drop chars
  through some protocols).
- `paste` → `robotgo.SetClipboard(text)` then `robotgo.KeyTap("v", "command")` (clipboard
  paste — the reliable path for long text/commands through RDP/VNC).
- `key` → parse `cmd/ctrl/shift/opt` + key → `robotgo.KeyTap`.
- A configurable post-action delay (`input.delay_ms`) lets the remote render before the next
  capture.

### 4.7 Config

`~/.config/screenreader/config.json`:
```json
{
  "version": 1,
  "app": "Microsoft Remote Desktop",
  "title": "",
  "mode": "app",
  "rect": { "x": 0, "y": 0, "w": 0, "h": 0 },
  "ocr":     { "languages": ["en"], "level": "accurate" },
  "capture": { "max_width": 1600 },
  "input":   { "delay_ms": 120 }
}
```
- `mode`: `"app"` (live window bounds) or `"rect"` (pinned rectangle).
- Every field has a sensible default; all are overridable per-call.
- Read fresh per action (small file); a `set_target` call or the config app rewrites it.

### 4.8 Error handling & safety

- **Errors:** window/app not found (→ suggest `list_windows`), permission denied (→ exact
  grant steps), OCR returned nothing, out-of-bounds coordinates, target not configured.
- **Safety:** this drives the user's **real cursor**. Mitigations:
  - Pinned-rect / window-relative geometry keeps actions within the target region.
  - `dry_run: true` on `click`/`drag` returns the computed absolute coordinates **without**
    executing.
  - Optional per-action logging (to stderr) for an audit trail.
  - The spec and README call out this risk explicitly.

### 4.9 Permissions & onboarding

macOS TCC requires, for the app that owns the server process (the terminal running opencode,
or opencode.app):
- **Screen Recording** — for region capture.
- **Accessibility** — for synthetic mouse/keyboard and window bounds.

First run triggers system prompts. `doctor` reports which are missing and how to grant them.
Onboarding steps are documented in the README.

## 5. Component 2 — `screenreader-config` (Swift/AppKit window app)

A small standalone **window** (≈ 360×280). No menu bar, `LSUIElement` so it doesn't add a
dock icon. Purpose: set the target the server will use.

**Controls**
- **App (process) name** text field.
- **Mode** segmented control: `App window` | `Rectangle`.
- **Select Rectangle…** button (enabled in Rectangle mode) → enters **rectangle mode**:
  a full-screen transparent overlay window; the user drags to draw the area; on release the
  `x, y, w, h` is captured and displayed.
- **Save** button → writes the config JSON.
- A **status line** showing the current target (app name or rect).

**Rectangle selection — coordinate correctness**
- AppKit global coordinates are **bottom-left origin (Y up)**; robotgo/Quartz capture+mouse
  are **top-left origin (Y down)**.
- The overlay view is `isFlipped = true` so drag coordinates are captured in **top-left /
  screen space directly** — the saved rect is already in the space the server uses. No flip
  bug in the persisted value.
- Multi-display: the overlay covers the screen(s); coordinates are stored in the global
  top-left space (consistent with `GetBounds`). v1 targets the primary display layout the
  user draws on.

**Behavior**
- On launch: load existing config into the fields.
- On Save: write `~/.config/screenreader/config.json` (create dirs as needed), preserving
  unrelated keys. The running server picks it up on its next action.

## 6. Testing strategy

- **Unit (no screen needed):**
  - Coordinate mapping: image↔screen, incl. Retina (2×) and letterboxed-region cases.
  - OCR `boundingBox` (normalized bottom-left) → image pixels → screen-absolute conversion.
  - Config load/save/merge + defaults.
  - Key-combo parsing (`cmd+shift+p`, `return`, …).
  - Error formatting.
- **Integration smoke (manual, needs a live window + permissions on the dev Mac):**
  - `doctor` (permissions + target resolution).
  - `window_info` on a known window.
  - `capture` returns a valid PNG of the expected size.
  - `ocr` on a screenshot with known text (assert expected substrings + plausible boxes).
  - `click` / `type` / `paste` verified visually against the Remote Desktop.
- Interactive tests are labeled manual; automated CI covers the pure/unit layer.

## 7. Project layout

```
screen_reader/
  server/                          # Go MCP server (single binary)
    go.mod
    main.go                        # stdio MCP server entry; wires tools
    internal/
      config/                      # load/save/merge config.json, defaults
      window/                      # target resolution (app/title/rect), GetBounds, list
      capture/                     # region capture, downscale, DPI handling
      ocr/                         # tmc/apple Vision wrapper, bbox conversion
      input/                       # mouse/keyboard/clipboard, key parsing, delays, dry_run
      geom/                        # image<->screen + Vision bbox math (pure, unit-tested)
      tools/                       # MCP tool definitions + handlers
  config-app/                      # Swift/AppKit setup window
    Package.swift
    Sources/ScreenReaderConfig/    # app, panel view, rect overlay
  docs/superpowers/specs/          # this spec
  README.md                        # setup, permissions, usage, safety
```

## 8. Risks & open items

- **cgo build friction:** robotgo + tmc/apple require Xcode CLT and cgo; first build on a
  clean machine may need iteration. Mitigation: document `xcode-select --install`; keep CLI
  escape hatches.
- **tmc/apple maturity:** the Vision bindings are generated and have a working OCR example,
  but are less battle-tested than robotgo. Mitigation: isolate in `internal/ocr`; keep a
  Tesseract (`gosseract`) fallback behind an interface.
- **Per-character typing through some protocols:** `type` may drop chars; `paste` is the
  recommended path for anything non-trivial. Documented.
- **Real-cursor side effects:** mitigated by rect/window-relative geometry + `dry_run`;
  called out in README.
- **Retina/DPI + multi-display:** handled via proportional mapping + flipped overlay;
  validated in the integration smoke on the dev machine.

## 9. Out of scope (v1)

- Runtime control/preview panel in the config app (setup-only for now).
- Multi-target orchestration, recording/replay, scheduling.
- Non-macOS platforms.
