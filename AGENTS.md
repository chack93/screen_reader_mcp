# AGENTS.md

Single-binary Go **stdio MCP server** (module `screenreader`, code in `server/`) that lets an opencode agent capture and drive a Remote Desktop window on macOS via cgo (robotgo, AppleScript). The agent reads the screen with its **own vision**: `capture` returns a PNG and nothing else.

## Commands (run from `server/`)

```bash
CGO_ENABLED=1 go build -o screenreader .   # output is gitignored
VERSION=v1.2.3 ./release.sh               # signed+notarized dmg in dist/ (Developer ID + notarytool creds; see docs/reference.md "Release builds")
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=1 go test ./...                # full suite (tools/input need cgo to compile)
go test ./internal/geom/ ./internal/config/ ./internal/keycombo/ ./internal/window/  # fast loop, cgo-free
```

- `CGO_ENABLED=0` fails the full build/test: `internal/perms` (and robotgo users) are cgo-only.
- No CI, Makefile, or linter config — gate changes on `go vet` + `go test`; keep code gofmt-clean.

## Layout

- `server/main.go` — registers the 15 MCP tools (three `Register*` funcs in `internal/tools`, the only file touching the MCP SDK). Reading is one tool (`capture`).
- `internal/window` — AppleScript via `osascript` (cgo-free); `internal/capture`, `internal/input` — cgo (robotgo); `internal/geom`, `internal/config`, `internal/keycombo` — pure logic.
- Config lives at `~/.config/screenreader/config.json` and is **re-read on every action** — live edits apply without restarting the server. Per-call args override config.

## Conventions & gotchas

- Coordinates are **screen-absolute, top-left origin, Y down** everywhere. `capture` may downscale or Retina-double the image — always map image→screen via `geom.MapImageToScreen` (pass `img_w`/`img_h`); input tools expose this as `coords: "image"`.
- Reading is vision-only: `capture.max_width` defaults to **0 = native resolution** because the PNG is the model's only reading channel (a negative per-call `max_width` also forces native).
- AppleScript in `internal/window`: never name a local `lines` (AppleScript treats it as a built-in text property → error -10006); quote app names with `appleQuote` (double embedded quotes); window titles may contain tabs (parsing takes first + last-4 fields, see `splitWindowLine`).
- Tests are pure logic only (no live screen, no TCC). Real capture/input verification needs Screen Recording, Accessibility, and Automation granted to the **hosting app**, plus a restart of that app after each grant (black capture = missing Screen Recording; silent input = missing Accessibility). `SR_DEBUG=1` dumps every osascript script + output to stderr for window-resolution debugging.
- The Swift menu-bar app is implemented per `docs/superpowers/specs/2026-10-06-screenreader-app-design.md` and `docs/superpowers/plans/2026-10-06-screenreader-app.md`: `app/` is a Swift package (`swift build`/`swift test`), `./build.sh` assembles + signs `app/build/Screen Reader MCP.app` embedding the Go binary and the icon (`app/Resources/AppIcon.icns`, authored from `app/Resources/AppIcon.svg`); the app is named **Screen Reader MCP** (bundle ID stays `dev.chack.screenreader`, executable `Screen Reader MCP`) while the embedded Go binary + MCP server name stay `screenreader`; the app spawns `screenreader --http` (loopback Streamable HTTP on :18427, bearer token in config `http.token`) and owns the TCC grants. The app mutates only `app`/`title`/`mode`/`rect`/`http`/`hotkey` in config.json and preserves the other keys.
- The global **stop hotkey** (config `hotkey`, default `f9`, `"off"` disables it): grammar + kVK table in `ScreenReaderKit/Hotkey.swift` (pure, unit-tested), Carbon registration in `ScreenReader/HotkeyCenter.swift`, click-to-record control in `ScreenReader/HotkeyRecorder.swift` (a **local** event monitor, so no extra grant). Carbon `RegisterEventHotKey` is deprecated but deliberate: `NSEvent.addGlobalMonitorForEvents`/`CGEventTap` both need Accessibility (in practice Input Monitoring), which read-only users lack. Fire → `ServerProcess.stop()` — the single stop choke point, deduped; never hook `killChild()`, which also serves restarts. The Go `Config.Hotkey` field is carried but never read; it exists so `set_target`'s Save cannot erase the app's key. Caveat: a bare F-key is a media key unless "Use F1, F2, … as standard function keys" is on, so the default F9 silently never fires on such Macs.
- Tools drive the **real cursor** unsandboxed; when testing input, prefer `dry_run: true` and keep the target pinned to the Remote Desktop window.
- The app is signed with the **ScreenReader Dev** self-signed identity (login keychain, cert SHA-1 `b705e67e883aeee9b5866c20188bcd582dcc2d1b`, created 2026-10-07): TCC grants are cert-anchored and **survive rebuilds** — `./build.sh` + relaunch needs no re-grant. If the keychain/key is ever lost, recreate via the openssl `-legacy` recipe in the plan's Task 6, rebuild, and grant once more.
