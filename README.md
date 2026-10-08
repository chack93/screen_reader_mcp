# ScreenReader MCP

## What it is

`screenreader` is a single-binary Go **stdio MCP server** that lets an opencode agent **see** and **drive** a Remote Desktop window on your Mac — so the agent can read files and do research on a remote machine where opencode itself can't run.

It treats the Remote Desktop **window** as a controllable "virtual screen": it captures the window's rendered pixels for the model to read, and synthesizes real mouse/keyboard events at the window's screen location, which the Remote Desktop client forwards to the remote machine. Protocol-agnostic by design — it works at the macOS OS/window level, so RDP, VNC, Chrome Remote Desktop, Parsec, TeamViewer, etc. all work identically. Nothing is installed on the remote machine.

## Status

The server is complete: all 18 tools are registered and verified over stdio (ListTools smoke passed). Live on-screen verification of capture/OCR/synthetic input requires the three macOS permissions below — grant them before expecting non-black captures or working input.

## ScreenReader app (recommended)

The **ScreenReader** menu-bar app hosts the server for you and owns the macOS
permissions — grant them once to the app instead of to every terminal.

```bash
./build.sh                       # builds server + app → app/build/ScreenReader.app
open app/build/ScreenReader.app
```

1. First run: click **Grant access** and approve the three prompts (Screen
   Recording, Accessibility, Automation). Confirm with menu → **Test capture…**.
2. Open the window → pick the target: select a window from the list, draw a
   rectangle over the screen, or pick a whole display (Screen).
3. **Copy opencode config** → merge the snippet into your `opencode.json` under
   `mcp.servers`. Any agent then reaches the MCP endpoint at
   `http://127.0.0.1:18427/mcp` while the app is open.

Notes:

- The endpoint is **loopback-only** and protected by a bearer token stored in
  `~/.config/screenreader/config.json` (`http.token`) — rotate it by editing the
  file; it takes effect on the next request. Keep the snippet in the app window
  in sync after rotating.
- Quitting the app stops the MCP endpoint (start the app again to serve).
- One-time identity (recommended): Keychain Access → Certificate Assistant →
  Create a New Certificate → name **ScreenReader Dev**, kind Code Signing,
  self-signed, login keychain. Without it, builds are ad-hoc-signed and macOS
  asks for permissions again after every rebuild.
- The app writes only `app`/`title`/`mode`/`screen`/`rect`/`http` keys to the
  shared config; `ocr`/`capture`/`input` edits keep working.
- Build/test: `cd app && swift build && swift test` (Kit logic is unit-tested;
  the GUI + TCC behavior is verified via the menu → Test capture canary).

## Prerequisites

- macOS (with the Remote Desktop client window you want to drive visible on screen)
- Go (module floor 1.26; older local Go works via automatic toolchain download)
- Xcode Command Line Tools: `xcode-select --install` (required for cgo)

## Build

```bash
cd server && CGO_ENABLED=1 go build -o screenreader .
```

This produces `server/screenreader` (already in `.gitignore`).

## Permissions

macOS TCC requires grants for the app that owns the server process (the terminal running opencode, or opencode.app). In **System Settings → Privacy & Security**, grant all three:

| Permission | Enables | Symptom when missing |
| --- | --- | --- |
| **Screen Recording** | Non-black `capture` output | Black image from `capture` |
| **Accessibility** | Synthetic mouse/keyboard input | No visible input when clicking/typing |
| **Automation** (System Events) | `list_windows`, `focus_window`, window bounds | `osascript:` errors |

After granting any permission, **quit and restart the hosting app** (terminal/opencode) so it picks up the grant.

## Configure the target

Pick one (or mix — per-call args always override config):

1. **Edit the config file** `~/.config/screenreader/config.json`:

```json
{
  "version": 1,
  "app": "Microsoft Remote Desktop",
  "title": "",
  "mode": "app",
  "screen": 0,
  "rect": { "x": 0, "y": 0, "w": 0, "h": 0 },
  "ocr": { "languages": ["en"], "level": "accurate" },
  "capture": { "max_width": 1600 },
  "input": { "delay_ms": 120 }
}
```

2. **Call the `set_target` tool** — sets and persists the default target (app/title, a pinned rect with `mode: "rect"`, or a whole display with `mode: "screen"` + `screen: <index>`). Pass `clear_app`/`clear_title` to unset the stored app target or title filter.
3. Use `list_windows` to discover the exact app name / title regex / bounds to pin, or `list_screens` to see the display indexes (0 = main) for a screen target.

## opencode config

Add the MCP server entry to `~/.config/opencode/opencode.json` (or the project's `opencode.json`) — replace the command path with your build location:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "screenreader": {
      "type": "local",
      "command": ["/Users/chack/syncthing/workspace/screen_reader/server/screenreader"]
    }
  }
}
```

opencode spawns the binary and talks to it over stdio.

## HTTP mode (manual)

Besides stdio, the server can serve MCP over **Streamable HTTP** on a loopback address:

```bash
cd server && CGO_ENABLED=1 go build -o screenreader .
./screenreader --http
# → screenreader serving MCP over Streamable HTTP on http://127.0.0.1:18427/mcp
```

The address and bearer token come from the `http` section of `~/.config/screenreader/config.json`:

```json
"http": { "addr": "127.0.0.1:18427", "token": "..." }
```

- `addr` must be loopback (127.0.0.1 or ::1); anything else is refused at startup.
- With `token` set, clients must send `Authorization: Bearer <token>`; token edits apply on the next request. Without a token the endpoint is unauthenticated (a warning is logged).
- `GET /health` reports liveness without auth (the menu-bar app uses it).
- In opencode: `"type": "remote"`, `"url": "http://127.0.0.1:18427/mcp"`, `"oauth": false`, plus the `Authorization` header when a token is set.

The recommended way to run HTTP mode is the ScreenReader menu-bar app (see below).

## Tools

| Tool | Purpose | Key args |
| --- | --- | --- |
| `capture` | Capture the target (window, pinned region, or whole screen) as a PNG image, with region bounds and image size. | `app`, `title`, `rect`, `max_width` |
| `ocr` | OCR the target (window, pinned region, or whole screen) with Apple Vision; returns text and screen-absolute boxes. | `app`, `title`, `rect`, `languages`, `level`, `include_boxes` |
| `click_text` | OCR-locate text in the target and click its center (screen-absolute). | `text`, `match`, `occurrence`, `dry_run` |
| `click` | Click at screen-absolute (x,y). | `x`, `y`, `button`, `clicks`, `dry_run`, `coords`, `rect`, `img_w`, `img_h` |
| `move` | Move the cursor to (x,y). | `x`, `y`, `coords`, `rect`, `img_w`, `img_h` |
| `scroll` | Scroll by (dx,dy) at (x,y) if given. | `dx`, `dy`, `x`, `y`, `coords`, `rect`, `img_w`, `img_h` |
| `drag` | Drag from (x1,y1) to (x2,y2). | `x1`, `y1`, `x2`, `y2`, `button`, `dry_run`, `coords`, `rect`, `img_w`, `img_h` |
| `type` | Type text via keystrokes. | `text` |
| `paste` | Paste text (clipboard + Cmd+V); reliable for long text/commands. | `text` |
| `key` | Press a key/shortcut combo, e.g. `"cmd+v"`, `"return"`. | `combo` |
| `wait` | Sleep for ms milliseconds. | `ms` |
| `set_target` | Set + persist the default target (app/title, pinned rect, or whole screen). | `app`, `title`, `rect`, `mode` (app\|rect\|screen), `screen`, `clear_app`, `clear_title` |
| `focus_window` | Activate the target app and raise its window; return bounds. | `app`, `title` |
| `window_info` | Report the resolved target (mode, screen, app, title, bounds). | — |
| `list_windows` | List visible windows (app, title, bounds) to choose a target. | — |
| `list_screens` | List active displays (index, main flag, screen-absolute bounds) to choose a screen target. | — |
| `wait_for_text` | Poll OCR until the text appears or timeout. | `text`, `timeout_ms`, `interval_ms`, `match`, `app`, `title`, `rect` |
| `doctor` | Self-check: displays, target resolution and permissions. | — |

## Coordinate model

- **Screen-absolute by default**: top-left origin, Y down. All input coordinates (click, move, drag) and OCR boxes are in these coordinates.
- `capture` returns `bounds={x,y,w,h}` (screen region) **and** `image=WxH` (pixel size, may be downscaled or Retina-doubled). Map image→screen proportionally: `screenX = bounds.x + (px / img_w) * bounds.w` (same for Y).
- `ocr` boxes are **screen-absolute** — usable directly with `click`/`drag` (with `include_boxes` to print them).
- Optional escape hatch: `coords: "image"` + `rect` on `click`/`move`/`drag`/`scroll` maps a point in captured-image space to screen space using the same proportional formula; pass `img_w`/`img_h` from `capture`'s `image=WxH` for correct Retina/downscale mapping (fall back to 1:1 when omitted). Coordinates are validated against the union of all active displays (the virtual screen) and rejected if out of range — so secondary-display coordinates (often negative or beyond the main display's size) are accepted.

## Safety

This server drives the **real cursor** on your desktop — it is not sandboxed from the rest of your screen. Mitigations:

- Use `dry_run: true` on `click`, `drag`, `click_text` to get the computed coordinates without executing.
- Pin a `rect` target (or a tightly-scoped app/title) to limit the blast radius to the Remote Desktop window.
- The agent's actions are **not** sandboxed from your desktop — keep the target pinned and review `dry_run` output for anything unexpected.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Black image from `capture` | Grant **Screen Recording**, then restart the hosting app. |
| No typing/clicking | Grant **Accessibility**, then restart the hosting app. |
| `osascript:` errors | Grant **Automation** (System Events), then restart the hosting app. |
| cgo build failure (`fatal error: 'objc.h' file not found` etc.) | Install Xcode Command Line Tools: `xcode-select --install`. |
| "no target configured" | Call `set_target` (e.g. `list_windows` first to find the app/title), or edit `~/.config/screenreader/config.json`. |

Remember: after granting any permission, **restart the app that owns the server process** — TCC grants are read at process start.
