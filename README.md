# Screen Reader MCP

A single-binary Go **MCP server** that lets an [opencode](https://opencode.ai) agent **see** and **drive** a Remote Desktop window on your Mac — so the agent can read files and do research on a remote machine where opencode itself can't run. It captures the window's pixels as PNGs (the model reads them with its own vision) and synthesizes real mouse/keyboard events at the window, which the Remote Desktop client forwards to the remote machine. Works with any protocol (RDP, VNC, Chrome Remote Desktop, Parsec, TeamViewer…); nothing is installed on the remote machine.

The server is complete: all 15 tools are registered and verified over stdio.

## Quick start (menu-bar app — recommended)

The **Screen Reader MCP** app hosts the server and owns the macOS permissions — grant them once to the app instead of to every terminal.

```bash
./build.sh                       # builds server + app → app/build/Screen Reader MCP.app
open "app/build/Screen Reader MCP.app"
```

1. First run: click **Grant access** and approve the three prompts (Screen Recording, Accessibility, Automation). Confirm with menu → **Test capture…**.
2. Pick the target: select a window from the list, draw a rectangle over the screen, or pick a whole display.
3. **Copy opencode config** → merge the snippet into your `opencode.json` under `mcp.servers`. Any agent then reaches the MCP endpoint at `http://127.0.0.1:18427/mcp` while the app is open.

## Quick start (manual build, stdio)

Requires Xcode Command Line Tools (`xcode-select --install`) for cgo.

```bash
cd server && CGO_ENABLED=1 go build -o screenreader .
```

Then point opencode at the binary in `~/.config/opencode/opencode.json`:

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

Grant the three macOS permissions (Screen Recording, Accessibility, Automation) to the app hosting the server, pick a target with `set_target`, and you're set — details in the [reference](docs/reference.md).

## Stop at any time

Press **F9** (default, rebindable) from any app to pause the agent instantly — every tool answers `offline`; see [Offline mode](docs/reference.md#offline-mode).

## Capture just the window

Point the server at a **window** and it renders **that window only** — the window server draws it, so anything floating on top is gone and it does not have to be frontmost. Point it at a drawn **rectangle** or a whole **display** and you get the screen exactly **as seen**, overlapping windows included; that is what those targets mean. An app-window capture that cannot be rendered is an error, never a silent downgrade to the region grab, and every result says which path produced the image (`source=window` / `source=region`, plus an `occluded=` note in window mode). Needs macOS 14+ for window capture, same Screen Recording grant — see [What each target captures](docs/reference.md#what-each-target-captures).

> **Warning:** this server drives the **real cursor** on your desktop. Keep the target pinned to the Remote Desktop window and use `dry_run` where in doubt — see [Safety](docs/reference.md#safety).

## Documentation

- **[docs/reference.md](docs/reference.md)** — everything technical: permissions, target configuration, HTTP mode, app details, stop hotkey, full tool reference, coordinate model, vision workflow, safety, release builds, troubleshooting.
- **[docs/superpowers/](docs/superpowers/)** — design specs and implementation plans (historical record).
