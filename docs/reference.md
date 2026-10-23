# Screen Reader MCP — Technical Reference

Setup details, configuration, tools, and operations. See the [README](../README.md) for the overview and quick start.

## Contents

- [Prerequisites](#prerequisites)
- [Build](#build)
- [Release builds (signed + notarized)](#release-builds-signed--notarized)
- [Permissions](#permissions)
- [Configure the target](#configure-the-target)
- [HTTP mode (manual)](#http-mode-manual)
- [App details](#app-details)
- [Stop hotkey](#stop-hotkey)
- [Tools](#tools)
- [Coordinate model](#coordinate-model)
- [Reading the screen (vision workflow)](#reading-the-screen-vision-workflow)
- [Safety](#safety)
- [Troubleshooting](#troubleshooting)

## Architecture notes

The server treats the Remote Desktop **window** as a controllable "virtual screen": it captures the window's rendered pixels for the model to read, and synthesizes real mouse/keyboard events at the window's screen location, which the Remote Desktop client forwards to the remote machine. Protocol-agnostic by design — it works at the macOS OS/window level, so RDP, VNC, Chrome Remote Desktop, Parsec, TeamViewer, etc. all work identically. Nothing is installed on the remote machine.

Reading is **vision-only**: `capture` returns a PNG and the model reads the text in it with its own vision, then converts what it sees into click coordinates.

## Prerequisites

- macOS (with the Remote Desktop client window you want to drive visible on screen)
- Go (module floor 1.26; older local Go works via automatic toolchain download)
- Xcode Command Line Tools: `xcode-select --install` (required for cgo)

## Build

```bash
cd server && CGO_ENABLED=1 go build -o screenreader .
```

This produces `server/screenreader` (already in `.gitignore`).

## Release builds (signed + notarized)

`./release.sh` builds the app in `dist/` (never touching the dev bundle in `app/build/`), signs every executable with the **Developer ID Application** identity (hardened runtime + secure timestamp), notarizes the zip and the final `.dmg`, staples both tickets, and emits `dist/screen-reader-mcp-<version>.dmg` + `.sha256`.

Locally it needs the Developer ID identity in the login keychain plus notarytool credentials — either a stored profile (`--keychain-profile screenreader`, the default) or explicit env vars:

```bash
NOTARY_KEY_PATH=…/AuthKey_XXXXXXXXXX.p8 NOTARY_KEY_ID=XXXXXXXXXX \
NOTARY_ISSUER_ID=xxxxxxxx-xxxx-… ./release.sh
```

**GitHub Actions:** pushing a *release* (any tag) triggers `.github/workflows/release.yml`, which runs `release.sh` on a macOS runner and attaches the `.dmg` to the release. Required repo secrets: `DEVELOPER_ID_P12`, `DEVELOPER_ID_P12_PASSWORD`, `NOTARY_API_KEY_P8`, `NOTARY_KEY_ID`, `NOTARY_ISSUER_ID`. The cert/key material itself lives outside the repo (in `~/Library/Application Support/ScreenReaderRelease/` on the dev box).

## Permissions

macOS TCC requires grants for the app that owns the server process (the menu-bar app, the terminal running opencode, or opencode.app). In **System Settings → Privacy & Security**, grant all three:

| Permission | Enables | Symptom when missing |
| --- | --- | --- |
| **Screen Recording** | Non-black `capture` output | Black image from `capture` |
| **Accessibility** | Synthetic mouse/keyboard input | No visible input when clicking/typing |
| **Automation** (System Events) | `list_windows`, `focus_window`, window bounds | `osascript:` errors |

After granting any permission, **quit and restart the hosting app** (terminal/opencode) so it picks up the grant. The menu-bar app avoids this churn: grants are cert-anchored to the **ScreenReader Dev** signing identity and survive rebuilds (see [App details](#app-details)).

## Configure the target

Config lives at `~/.config/screenreader/config.json` and is **re-read on every action** — live edits apply without restarting the server. Per-call args always override config.

Pick one target mode (or mix — per-call args always win):

1. **Edit the config file** `~/.config/screenreader/config.json`:

```json
{
  "version": 1,
  "hotkey": "f9",
  "app": "Microsoft Remote Desktop",
  "title": "",
  "mode": "app",
  "screen": 0,
  "rect": { "x": 0, "y": 0, "w": 0, "h": 0 },
  "capture": { "max_width": 0 },
  "input": { "delay_ms": 120 }
}
```

`capture.max_width: 0` (the default) captures at **native resolution**: the image is the model's only reading channel, so only set a width if payloads get too big for your model. A per-call `max_width` overrides it (`-1` forces native even when the config pins a width).

`hotkey` is the menu-bar app's global stop key (see [Stop hotkey](#stop-hotkey)); `"off"` disables it. The server never reads it — the field is in its config struct only so a `set_target` rewrite cannot drop it.

2. **Call the `set_target` tool** — sets and persists the default target (app/title, a pinned rect with `mode: "rect"`, or a whole display with `mode: "screen"` + `screen: <index>`). Pass `clear_app`/`clear_title` to unset the stored app target or title filter.
3. Use `list_windows` to discover the exact app name / title regex / bounds to pin, or `list_screens` to see the display indexes (0 = main) for a screen target.

## opencode config (stdio)

Add the MCP server entry to `~/.config/opencode/opencode.json` (or the project's `opencode.json`) — replace the command path with your build location:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "screenreader": {
      "type": "local",
      "command": ["/path/to/screen_reader/server/screenreader"]
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

The recommended way to run HTTP mode is the Screen Reader MCP menu-bar app (see [App details](#app-details)).

## App details

The menu-bar app (`app/`, built by `./build.sh` into `app/build/Screen Reader MCP.app`) spawns `screenreader --http` (loopback Streamable HTTP on `:18427`) and owns the TCC grants.

- The endpoint is **loopback-only** and protected by a bearer token stored in `~/.config/screenreader/config.json` (`http.token`) — rotate it by editing the file; it takes effect on the next request. Keep the snippet in the app window in sync after rotating.
- Quitting the app stops the MCP endpoint (start the app again to serve).
- The app writes only `app`/`title`/`mode`/`screen`/`rect`/`http`/`hotkey` keys to the shared config; `capture`/`input` edits keep working.
- Build/test: `cd app && swift build && swift test` (Kit logic is unit-tested; the GUI + TCC behavior is verified via the menu → Test capture canary).
- One-time identity (recommended): Keychain Access → Certificate Assistant →
  Create a New Certificate → name **ScreenReader Dev**, kind Code Signing,
  self-signed, login keychain. Without it, builds are ad-hoc-signed and macOS
  asks for permissions again after every rebuild. With it, TCC grants are
  cert-anchored and survive rebuilds — rebuild + relaunch needs no re-grant.

## Stop hotkey

The app registers a **global** hotkey that stops the MCP server: the lever you
reach for when an agent is driving your desktop and you want it to stop *now*,
without hunting for the menu bar icon. The default is **F9**.

- It only ever **stops**. It cannot start the server, so it can never be the
  reason a server is running — restarting is a deliberate act in the menu or the
  settings window. Pressing it while the server is down does nothing.
- It needs **no extra permission**. Registration goes through Carbon
  (`RegisterEventHotKey`), not an event tap, so it works for read-only users and
  never triggers an Input Monitoring prompt.
- **Rebind or disable it** in the settings window (*Keyboard shortcut* section:
  click the shortcut field, press the combo; **Clear** disables it). The value lives in
  `~/.config/screenreader/config.json` as `"hotkey"` — `"f9"`, `"ctrl+opt+f9"`,
  `"cmd+shift+k"`, or `"off"`. The grammar and modifier aliases
  (`cmd|command`, `ctrl|control`, `opt|option|alt`, `shift`) match the `key`
  tool. The menu shows the binding on the stop item (`Server: stop (⌃⌥F9)`), and
  a rebind takes effect immediately — no relaunch.
- **Bare F-keys can be media keys.** If *Use F1, F2, … as standard function
  keys* is off (System Settings → Keyboard), a plain F9 press is a media-key
  event and never reaches the hotkey. Hold **Fn** while recording the key, bind
  a ⌃/⌥ combo instead, or turn that setting on. The recorder says
  `media key — try Fn or ⌃/⌥` when this happens.
- If another app already owns the key exclusively, ours silently never fires and
  the app logs `could not register stop hotkey`. Pick another binding.

## Tools

| Tool | Purpose | Key args |
| --- | --- | --- |
| `capture` | Capture the target (window, pinned region, or whole screen) as a PNG image, with region bounds and image size. Read on-screen text from the image with your own vision. | `app`, `title`, `rect`, `max_width` |
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
| `doctor` | Self-check: displays, target resolution and permissions. | — |

## Coordinate model

- **Screen-absolute by default**: top-left origin, Y down. All input coordinates (click, move, drag) are in these coordinates.
- `capture` returns `bounds={x,y,w,h}` (screen region) **and** `image=WxH` (pixel size, may be Retina-doubled). Map image→screen proportionally: `screenX = bounds.x + (px / img_w) * bounds.w` (same for Y).
- Easiest path for a vision model: read the pixel position straight off the image you were given and pass `coords: "image"` + `rect` + `img_w`/`img_h` to `click`/`move`/`drag`/`scroll` — the server does the mapping for you. Coordinates are validated against the union of all active displays (the virtual screen) and rejected if out of range, so secondary-display coordinates (often negative or beyond the main display's size) are accepted.

## Reading the screen (vision workflow)

The captured PNG *is* the reading channel, so the loop is

1. `capture` → look at the image (pass `max_width: -1` and re-capture if small text is unreadable).
2. Read the pixel position of what you want to act on, then `click` it with `coords: "image"`, `rect` and `img_w`/`img_h` from the `bounds=… image=WxH` line.
3. To wait for something to happen (a dialog, a page load), `wait` then `capture` again and read the new image.

Because reading is visual, keep captures cheap: pin a tight `rect` or app/title target instead of capturing a whole display, and re-capture only after something changed.

## Safety

This server drives the **real cursor** on your desktop — it is not sandboxed from the rest of your screen. Mitigations:

- Press the **stop hotkey** (default **F9**) to kill the server instantly from any app — see [Stop hotkey](#stop-hotkey).
- Use `dry_run: true` on `click`, `drag` to get the computed coordinates without executing.
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
| Small text misread in a capture | Re-capture with `max_width: -1` (native pixels) and a tighter `rect`/app target, so the region fills more of the image. |
| Stop hotkey does nothing | Reopen the app (a build from before the feature has no hotkey), check `hotkey` isn't `"off"` in the config, and see the media-key caveat under [Stop hotkey](#stop-hotkey). |

Remember: after granting any permission, **restart the app that owns the server process** — TCC grants are read at process start.
