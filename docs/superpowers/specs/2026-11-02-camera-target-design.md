# Camera Target — Design Spec

- **Date:** 2026-11-02
- **Status:** Pending user review
- **Repo:** `screen_reader`
- **Platform target:** macOS 13+ (the app floor from `app/Package.swift`)
- **Extends:** `2026-10-03-screenreader-mcp-design.md` (Go engine: tools, config, coordinate model) and
  `2026-10-06-screenreader-app-design.md` (app: settings UI, TCC ownership, ConfigStore).

## 1. Purpose

Add a fourth target type — a **camera** (built-in webcam or any connected/external camera) — selectable
in the app's settings. While the target is a camera, the agent's `capture` tool returns a still frame
from that camera as a PNG, using its existing vision-only reading model. The camera is a *reading*
target: pointer input is blocked while it is selected.

Driving decisions (settled during brainstorming):

- **Occasional still frames**, not video streaming — one frame per `capture` call.
- **Selection is app-owned UI** (4th Target segment). The agent never switches to camera mode itself;
  `set_target` keeps rejecting `mode: "camera"`. The agent *does* read through it: `capture` honors
  `mode: "camera"` from config.
- **Cached capture session that expires on idle** (~30 s), mirroring `internal/sck`'s filter cache —
  fast repeat captures, camera light off during idle.
- **Pointer input (click/move/scroll/drag) is blocked** while the target is a camera; `type`/`paste`/
  `key`/`wait` keep working (they drive the focused app, not the target).

## 2. Goals / Non-goals

**Goals**

- `capture` returns camera frames (PNG + `source=camera` text line) when config says `mode: "camera"`.
- App settings: `App window | Rectangle | Screen | Camera` segmented control with a device picker
  (works with the server stopped), per-mode row conventions preserved.
- Camera TCC owned by the app (`NSCameraUsageDescription`); permission UI only while camera mode is
  active; grants anchor on the signing cert like the existing ones.
- Loud, actionable errors everywhere: device missing, access denied, camera in use — never a silent
  fallback to another camera or to a screen grab.
- `max_width` downscaling behaves identically for camera frames.
- Idle camera session torn down ≤ 30 s after the last frame; server stop (incl. the F9 hotkey) always
  releases the camera.

**Non-goals**

- No video streaming, frame bursts, motion detection, or background timers.
- No resolution/format picker (device default format + `max_width` is enough).
- No microphone/audio of any kind.
- No agent-facing camera tools (`list_cameras` etc.) and no `set_target mode=camera` — selection is
  app UI only.
- No camera preview in settings.
- No changes to region/window capture paths, input behavior outside camera mode, or access modes.

## 3. Architecture

```
┌────────────────────────────────────────────────────────────┐
│ Screen Reader MCP.app (Swift)                               │
│ • Target segment "Camera" + device picker (AVFoundation,    │
│   enumerated in-app → works while the server is stopped)    │
│ • Camera permission row (Permissions section, camera mode   │
│   only) + NSCameraUsageDescription in Info.plist            │
│ • writes config.json: mode="camera", camera={id,name}       │
└──────────────┬─────────────────────────────────────────────┘
               │ spawns (unchanged)
┌──────────────▼─────────────────────────────────────────────┐
│ Go binary "screenreader"                                    │
│ capture tool ── classifyTarget ──▶ kindCamera               │
│                        │                                    │
│              internal/camera (new, cgo/AVFoundation)        │
│  • List() / Capture(id) / AuthStatus()                      │
│  • one cached AVCaptureSession behind one mutex,            │
│    30 s idle expiry, bounded semaphore waits                │
│              │ *image.RGBA                                  │
│              ▼                                              │
│  capture.CaptureCamera → ScaleDown + PNG (shared path)      │
│ pointer input guard: click/move/scroll/drag error out       │
└─────────────────────────────────────────────────────────────┘
```

The camera light is on only inside `Capture` calls and the ≤ 30 s warm window — the session lives in
the server process, so stopping/restarting the server (or the stop hotkey) releases the camera.

## 4. Config contract

`~/.config/screenreader/config.json` gains:

```json
{
  "mode": "camera",
  "camera": { "id": "AB12CD34…", "name": "FaceTime HD Camera" }
}
```

- `mode` gains the value `"camera"` (existing `"app" | "rect" | "screen"` unchanged).
- `camera.id` — `AVCaptureDevice.uniqueID`. Apple documents it as persisting across device
  connect/disconnect, app restarts, and reboots, so it is the stable identity. Empty `""` =
  system-default video device. `camera.name` is display-only (picker/doctor text).
- **Ownership:** `camera` is app-owned like `hotkey`: the app writes it; the Go `Config` struct carries
  the field so `set_target`'s whole-file Save preserves it. `set_target` continues to accept only
  `app|rect|screen` for `mode`.
- Go config: `Camera CameraCfg` with `ID string \`json:"id"\`` / `Name string \`json:"name"\``.

## 5. Go engine changes

### 5.1 `internal/camera` (new package, cgo/AVFoundation, modeled on `internal/sck`)

Public surface:

- `List() ([]Device, error)` — `Device{ID, Name}` via `AVCaptureDeviceDiscoverySession`. Device types:
  macOS 14+: `[.builtInWideAngleCamera, .external, .continuityCamera]`; macOS 13:
  `[.builtInWideAngleCamera, .externalUnknown]` (deprecated on 14+, runtime-checked — the
  `SCScreenshotManager` availability pattern). Includes USB webcams and virtual cameras (they present
  as external); `.continuityCamera` covers iPhone Continuity Camera on 14+. Used only to name
  alternatives in error messages.
- `Capture(id string) (*image.RGBA, error)` — one settled frame from `id` (or the default device when
  `id` is `""`).
- `AuthStatus() string` — camera TCC status string for `doctor`; starts nothing (no light).

Session model (one mutex serializes all AVFoundation use, exactly as in `sck`):

- Cache: `{deviceID, session, output queue, latest frame, lastUsed}`. `Capture` with the cached ID and
  `expired(lastUsed, now, 30s) == false` returns the latest buffered frame (near-instant, light
  already on).
- Otherwise: stop the old session (if any), start fresh — `AVCaptureSession` + `AVCaptureDeviceInput` +
  `AVCaptureVideoDataOutput` (`kCVPixelFormatType_32BGRA`) on a dedicated serial dispatch queue whose
  delegate stores the newest sample buffer.
- **Warm-up on fresh start:** wait for the first frame, then ~0.5 s more for auto-exposure/white
  balance to settle, then return the newest buffer — the agent never gets a black/gray frame.
- **Idle teardown is timer-driven:** each `Capture` (re)schedules a `time.AfterFunc(30·s)` that stops
  the cached session — so the camera is released ~30 s after the *last frame*, not merely on the next
  call (lazy expiry alone would leave the light on forever). `Capture` also re-checks
  `expired(lastUsed, now, 30s)` itself; both paths share that one pure predicate, and teardown takes
  the same mutex, so there is no race with a bounded wait.
- Every wait is a bounded `dispatch_semaphore` (10 s start cap, 2 s per-frame wait) — handlers run on
  background queues (verified for SCK; camera delegates are queue-based by construction), so a wedged
  camera errors, never hangs the server.

Pure-Go cores (fast-loop unit-testable, no TCC): BGRA→RGBA conversion over `[]byte`;
`expired(lastUsed, now, ttl) bool`. The cgo file stays thin glue.

Error contract (loud, names the way out — mirrors window-capture policy):

| Failure | Message gist |
| --- | --- |
| `id` not found | `camera "<name>" not connected (available: A, B)` — never silently grabs another camera |
| No devices at all | `no cameras found` |
| TCC denied/restricted | `camera access denied — grant: System Settings → Privacy & Security → Camera (restart Screen Reader MCP after granting)` |
| Session start fails | `camera: <device>: <nserror> — possibly in use by another app; quit it or pick another camera` |
| Frame timeout | `camera: timed out waiting for a frame from <name> (is it in use by another app?)` |

### 5.2 `internal/capture`

`CaptureCamera(id string, maxWidth int) (*Result, error)` — wraps `camera.Capture`, applies
`geom.ScaleDown` + the shared PNG encode, returns `Result` with **zero `Bounds`** (camera frames have
no screen region).

### 5.3 `internal/tools`

- `targetKind` gains `kindCamera`; `targetSpec` gains `CameraID`.
- `classifyTarget`: after the explicit `rect` and per-call `app`/`title` overrides (which keep winning,
  unchanged semantics), `cfg.Mode == "camera"` → `kindCamera` **before** the stored-app fallback — a
  stale `cfg.App` never silently retargets a camera-mode user.
- `targetSpec.rect()` for `kindCamera` → error `camera target has no screen bounds`.
- `capture`: `kindCamera` path calls `capture.CaptureCamera`; text line
  `image=%dx%d source=camera device=%q` (no `bounds=` token; nothing maps to screen coords). The
  existing `window:true` non-app rejection already names the kind and covers camera for free. Tool
  description gains one sentence: camera mode returns a frame from the configured camera, image
  coordinates do not map to screen coordinates.
- **Pointer-input guard:** `click`/`move`/`scroll`/`drag` first check `loadCfg().Mode == "camera"` and
  return `click is disabled: the target is a camera — set a window/rect/screen target in Screen Reader
  settings` (one shared helper, e.g. `cameraInputErr(toolName)`). `type`/`paste`/`key`/`wait`
  untouched.
- `window_info`: when `mode == "camera"`, reports `mode=camera camera="<name>" capture=camera` instead
  of resolving bounds; an empty stored name prints `camera="(system default)"`.
- `focus_window`, `list_windows`, `list_screens`: unchanged — they address apps/displays, not the
  target mode.
- `doctor`: config line already prints `mode`; when `mode == "camera"` add
  `  camera: ok ("<name>", auth=authorized)` / `  camera: MISSING — …` using `camera.AuthStatus()` +
  `camera.List()` presence check — **no session start** (a check-up must not light the LED). The
  `target:` line prints `target: camera "<name>"` instead of bounds. `window_capture` and the three
  existing permission lines stay as-is.
- `set_target`: rejects `mode: "camera"` (already does — invalid mode error); its Save preserves
  `camera` via the new struct field.
- **Access modes:** no new tools → `readOnlyTools` unchanged; camera capture works under `read_only`
  (pointer tools aren't registered there anyway).

## 6. App changes

### 6.1 `ScreenReaderKit/ConfigStore.swift`

- `Target` gains `cameraID`, `cameraName`; `applyTarget` writes the `camera` dict; `target(from:)`
  reads it (absent → `""`/`""`). Doc comment's owned-key list gains `camera`.

### 6.2 `ScreenReaderKit/Permissions.swift`

- `TCCPermission` gains `case Camera` ("Camera"): `isGranted` via
  `AVCaptureDevice.authorizationStatus(for: .video) == .authorized`; `request()` via
  `requestAccess(for: .video)`. The app-side request is the reliable prompt path (attributed to the
  app, the responsible process); the Go child inherits the grant, like Screen Recording.

### 6.3 `ScreenReader/SettingsWindow.swift`

- Segmented control → `["App window", "Rectangle", "Screen", "Camera"]`; `refresh()` mode-key mapping
  and `modeChanged` gain the `"camera"` leg.
- New `cameraRow` = popup + Refresh, mirroring `screenRow`/`syncScreenPicker`: enumerate devices in
  Swift (same device-type list as §5.1, so the picker works with the server stopped), rebuild only on
  fingerprint change (joined uniqueIDs), first item `(System default camera)` → `id: ""`.
- Stored `id` not in the enumeration → insert placeholder `(not connected: <name>)`, select it, leave
  config untouched — loud, never silently retargeted.
- `modeChanged`: switching to Camera persists `mode: "camera"` immediately and adopts the picker's
  current selection (the T17 app-mode adoption pattern).
- Permissions section: the Camera row is shown only while stored `mode == "camera"` (row show/hide +
  `fitToContent()` already handle this); `grantAll` includes it in that mode. `refresh()` re-evaluates
  the row list per tick.

### 6.4 `app/Info.plist`

- Add `NSCameraUsageDescription`: "Screen Reader MCP captures a still frame from the camera you choose
  as the reading target." (No camera usage without it; a missing key is a hard failure, not a prompt.)

## 7. Testing

**Go fast loop (pure logic, `CGO_ENABLED=0`-compatible packages only where they already are):**

- `classifyTarget`: `mode=camera` → `kindCamera`; explicit `rect`/`app`/`title` overrides beat camera;
  stale `cfg.App` does not retarget camera mode.
- Pointer-input guard helper: blocks in camera mode, allows otherwise; type/paste/key/wait unaffected.
- `config`: Load/Save round-trip keeps `camera` intact; `set_target`-style Save preserves it.
- `camera`: BGRA→RGBA conversion (byte-exact incl. rows with odd padding via a pure helper);
  `expired()` boundary cases — one predicate shared by the in-`Capture` check and the idle-teardown
  timer, so the two can't drift.
- capture text line: camera result renders `image=WxH source=camera device="…"` without `bounds=`.
- `set_target mode:"camera"` still rejected.

**Swift (`swift test`):** ConfigStore camera round-trip; `applyTarget` preserves foreign keys;
`TCCPermission` includes Camera.

**Live verification (manual — needs camera grant to the app + app restart after granting):**

1. Pick camera → `capture` returns a real frame; text line has `source=camera`.
2. Second capture within 30 s is visibly faster (warm session).
3. Green light off after > 30 s idle.
4. `camera` in use by another app → error names the possibility, server stays alive.
5. Unplug the chosen camera → capture error names it + lists available devices; app picker shows
   `(not connected: …)`.
6. `doctor` camera line correct without lighting the LED.
7. `read_only` + camera mode: `capture` works, pointer tools absent.
8. F9 stop hotkey → camera released (light off).

**Docs:** AGENTS.md (camera mode conventions: `source=camera`, app-owned `camera` key, pointer guard,
session lifetime) and `docs/reference.md`.

## 8. Implementation-time verification checklist

Facts to confirm in the first build/spike task (design assumes but implementation must verify):

- Camera TCC prompt for the Go child process attributes to **Screen Reader MCP** (responsible process),
  grant survives rebuilds via the ScreenReader Dev cert — as documented for Screen Recording.
- `AVCaptureDevice.default(...)` (used when `id == ""`) matches the user's system-default camera on
  macOS 13 and 14+.
- `localizedName` values before the camera grant are usable for the app's picker (enumerate without
  starting a session).
- 0.5 s post-first-frame settle is enough for typical webcams in dim light (adjust the constant once,
  in one place).
- No new signing entitlement is required for camera under the hardened runtime (TCC-only).

## 9. Decisions log

| Decision | Choice | Why |
| --- | --- | --- |
| Frame model | Still per `capture` call | Matches vision-only reading; streaming rejected |
| Who selects | App UI only | Camera is the user's privacy choice, not the agent's |
| Session | Cached, 30 s idle expiry | Fast repeats; light off when idle; mirrors `sck` cache |
| Input in camera mode | Block pointer, allow keyboard | No screen coords to aim at; keyboard still serves the watched app |
| Device identity | `uniqueID` (+ display name) | Apple-documented persistence across replugs |
| Missing device | Loud error, no fallback | Same policy as window capture ("clean image" promise) |
| Backend location | `internal/camera` in the Go server | Single capture choke point; app stays settings-only (approach A) |
