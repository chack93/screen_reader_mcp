# Offline Mode (keep-alive stop) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** "Stop" (menu, F9, 4 h timer, settings button) pauses the agent via a live config flag instead of killing the Go child, so the MCP connection never drops while the menu-bar app is running — stopped means every tool answers **"offline"**.

**Architecture:** New boolean `offline` key in `~/.config/screenreader/config.json` (same hot-reload rails as `access`). A per-call guard in `internal/tools` refuses every tool while it is set and releases the camera; the Swift app routes all four stop/start entry points through one `AppDelegate` choke point that only writes that flag. The server process runs **iff** the app runs — no UI path kills it anymore.

**Tech Stack:** Go 1.x (cgo: robotgo/AVFoundation), `github.com/modelcontextprotocol/go-sdk/mcp`; Swift 5 (AppKit, XCTest via SwiftPM).

**Spec:** `docs/superpowers/specs/2026-11-19-offline-mode-design.md` — the lifecycle invariant (§2), UI state table (§4.3) and fail-closed rule (§5) argue from there.

## Global Constraints

- Go commands run from `server/` and **require `CGO_ENABLED=1`** (e.g. `CGO_ENABLED=1 go test ./...`); `CGO_ENABLED=0` fails the full build. Fast loop for cgo-free packages: `go test ./internal/config/`.
- Gate every Go change on `CGO_ENABLED=1 go vet ./...` and the test suite; keep code gofmt-clean.
- Swift commands run from `app/`: `swift build && swift test`. App floor macOS 13 (`app/Package.swift`) — no `Synchronization`/macOS 15 APIs.
- Config key is exactly `"offline"` (Go `Offline bool`, json `offline`, Swift `ConfigStore.offline`); default/absent = `false` = online.
- The agent-visible offline status is **exactly** this string (tests assert on it): `offline — server stopped from the Screen Reader app (start it from the menu bar item, or set "offline": false in ~/.config/screenreader/config.json)`
- The app mutates only: `app`/`title`/`mode`/`screen`/`rect`/`camera`/`http`/`access`/`hotkey` **+ `offline`** (this plan extends the list).
- Do not change `/health` behavior (200 = process healthy) or the registered tool list while offline.
- Commit style: `feat(...)` / `docs(...)` / `chore(app):`, one commit per task.

---

### Task 1: Go config — `Offline` field

**Files:**
- Modify: `server/internal/config/config.go` (struct at line 43-59, `Defaults()` at 68-80)
- Test: `server/internal/config/config_test.go` (append after `TestSaveLoad_accessRoundTrip`, ~line 240)

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config.Offline bool` (json `"offline"`, default `false`) — Task 2's guard reads it per call; Task 3+ treat it as the app's stop switch. `Save` must round-trip it (so `set_target` rewrites never erase the app's flag).

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/config/config_test.go` (mirror of the `access` tests there; `tempDir(t)` helper already exists in that file):

```go
func TestDefaults_offline(t *testing.T) {
	if c := Defaults(); c.Offline {
		t.Fatalf("default offline = true, want false")
	}
}

func TestLoad_offline(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"offline":true}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !c.Offline {
		t.Fatalf("offline = false, want true")
	}
}

// TestSaveLoad_offlineRoundTrip: "offline" belongs to the menu-bar app, but
// it must survive a server-side Save (set_target rewrites the whole file).
func TestSaveLoad_offlineRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.Offline = true
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Offline {
		t.Fatalf("round-trip offline = false, want true")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd server && go test ./internal/config/ -run offline -v`
Expected: compile error `c.Offline undefined` (or test failures).

- [ ] **Step 3: Implement**

In `server/internal/config/config.go`, add the field to `Config` directly after `Access` (line 45):

```go
	// Offline is the menu-bar app's stop switch: true = every tool call is
	// refused with the "offline" status (checked per call in internal/tools).
	// Carried through Save like Hotkey/Camera so set_target rewrites keep it.
	Offline bool `json:"offline"`
```

`Defaults()` needs no change (zero value `false` = online).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/config/ -run offline -v` → PASS (3 tests).
Then: `CGO_ENABLED=1 go vet ./...` from `server/` → clean.

- [ ] **Step 5: Commit**

```bash
git add server/internal/config/config.go server/internal/config/config_test.go
git commit -m "feat(config): offline kill-switch key (default false)"
```

---

### Task 2: Go — offline guard on every tool + `camera.Close()` + doctor line

**Files:**
- Modify: `server/internal/camera/camera.go` (add `Close()` after `AuthStatus()`, ~line 294)
- Modify: `server/internal/tools/access.go` (new guard + wire into `add()` at line 65-70)
- Modify: `server/internal/tools/tools.go:618` (doctor config line)
- Test: `server/main_test.go` (append 3 tests; add `"os"` to imports)

**Interfaces:**
- Consumes: `config.Config.Offline` from Task 1.
- Produces: `camera.Close()` (exported, idempotent, mutex-guarded) and package-private `offlineGuard() error` in `internal/tools`; every tool registered through `add()` refuses with the offline status while the flag is set. Task 4/5 and the docs describe this as the "offline mode".

- [ ] **Step 1: Add `camera.Close()` to `server/internal/camera/camera.go`**

Insert after `AuthStatus()` (no unit test: the file is cgo/AVFoundation; the existing pure tests must stay green):

```go
// Close releases a cached session immediately — "stop" must turn the camera
// light off (spec §4.1) — and is idempotent, so the tools offline guard can
// call it on every refused call. Takes mu (the same lock Capture holds), so
// it can never pull a session out from under a live frame grab, and cancels
// the idle timer since there is nothing left for it to tear down.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if idle != nil {
		idle.Stop()
	}
	if active != nil {
		C.sr_cam_stop(active)
		active = nil
		activeID = ""
	}
}
```

Run: `cd server && CGO_ENABLED=1 go build ./internal/camera/` → success; `go test ./internal/camera/` → PASS (pure tests unaffected).

- [ ] **Step 2: Write the failing guard tests**

Append to `server/main_test.go` and add `"os"` to its import block:

```go
// TestOfflineGuard_allTools: while "offline" is set the tool LIST is
// unchanged (offline never filters registration) and representative tools of
// all three Register* groups answer with the offline status instead of doing
// real work.
func TestOfflineGuard_allTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Offline = true
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"wait", "capture", "doctor"} { // Reading/Input/Target registrars
		args := map[string]any{}
		if name == "wait" {
			args = map[string]any{"ms": 1} // per-tool schemas reject foreign props (see TestClick_cameraModeBlocked)
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s call: %v", name, err)
		}
		if !res.IsError {
			t.Fatalf("%s ran while offline: %v", name, res.Content)
		}
		if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "offline —") {
			t.Fatalf("%s: %q", name, text)
		}
	}
	if got := listToolNames(t); !sameNames(got, fullToolNames) {
		t.Fatalf("offline tool set:\n got %v\nwant %v", got, fullToolNames)
	}
}

// TestOfflineGuard_liveResume: a session registered while online keeps its
// tool list when the flag flips, refuses calls while offline, and resumes
// them when it clears — no reconnect (mirror of TestAccessGuard_blocksStaleSession).
func TestOfflineGuard_liveResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	ctx := context.Background()
	save := func(offline bool) {
		t.Helper()
		cfg := config.Defaults()
		cfg.Offline = offline
		if err := config.Save(config.DefaultPath(), cfg); err != nil {
			t.Fatalf("save config: %v", err)
		}
	}
	save(true)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"ms": 1}})
	if err != nil {
		t.Fatalf("wait call: %v", err)
	}
	if !res.IsError {
		t.Fatal("wait ran while offline")
	}
	save(false)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"ms": 1}})
	if err != nil {
		t.Fatalf("wait call after resume: %v", err)
	}
	if res.IsError {
		t.Fatalf("wait refused after resume: %v", res.Content)
	}
}

// TestOfflineGuard_unreadableConfig: the kill switch FAILS CLOSED — a config
// that cannot be parsed rejects calls instead of silently resuming work.
func TestOfflineGuard_unreadableConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	if err := os.WriteFile(config.DefaultPath(), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"ms": 1}})
	if err != nil {
		t.Fatalf("wait call: %v", err)
	}
	if !res.IsError {
		t.Fatal("wait ran with an unreadable config")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "unreadable") {
		t.Fatalf("text %q must say the config is unreadable", text)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd server && CGO_ENABLED=1 go test . -run Offline -v`
Expected: FAIL — `wait ran while offline` (no guard yet).

- [ ] **Step 4: Implement the guard in `server/internal/tools/access.go`**

Add `"errors"` and `"screenreader/internal/camera"` to the imports, then add below `accessGuard`:

```go
// errOffline is the status every tool returns while the app has stopped the
// server (config key "offline"). The text IS the status the agent sees.
var errOffline = errors.New(`offline — server stopped from the Screen Reader app (start it from the menu bar item, or set "offline": false in ~/.config/screenreader/config.json)`)

// offlineGuard is the kill switch, checked before accessGuard on every call:
// while "offline" is set no tool does real work, and the refusal releases the
// camera (Close is an idempotent no-op when nothing is open) so the light
// goes off even if the caller keeps hammering tools. An unreadable config
// FAILS CLOSED — a corrupt file must never silently re-enable real clicks
// after the user pressed Stop (deliberate divergence from accessGuard's
// fail-open, which only gates which tools run).
func offlineGuard() error {
	cfg, err := config.Load(config.DefaultPath())
	switch {
	case err != nil:
		camera.Close()
		return fmt.Errorf("server refused the call: config.json is unreadable (%v) — fix or remove it, then start the server from the app", err)
	case cfg.Offline:
		camera.Close()
		return errOffline
	}
	return nil
}
```

Wire it into `add()` — the wrapped handler becomes (offline first: it overrides any access mode):

```go
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, a A) (*mcp.CallToolResult, any, error) {
		if e := offlineGuard(); e != nil {
			return errResult(e), nil, nil
		}
		if e := accessGuard(name); e != nil {
			return errResult(e), nil, nil
		}
		return h(ctx, req, a)
	})
```

- [ ] **Step 5: Doctor's config line reports the flag**

`server/internal/tools/tools.go:618` — replace:

```go
			out += fmt.Sprintf("config: access=%s mode=%s screen=%d app=%q title=%q rect=%+v\n", cfg.Access, cfg.Mode, cfg.Screen, cfg.App, cfg.Title, cfg.Rect)
```

with:

```go
			out += fmt.Sprintf("config: access=%s offline=%v mode=%s screen=%d app=%q title=%q rect=%+v\n", cfg.Access, cfg.Offline, cfg.Mode, cfg.Screen, cfg.App, cfg.Title, cfg.Rect)
```

(No test pins this line; `doctor` is refused while offline anyway — it is for post-resume diagnosis.)

- [ ] **Step 6: Run the full gate**

Run: `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...`
Expected: PASS — new 3 tests green, all pre-existing tests green (esp. `TestAccessGuard_blocksStaleSession`, `TestClick_cameraModeBlocked`).

- [ ] **Step 7: Commit**

```bash
git add server/internal/camera/camera.go server/internal/tools/access.go server/internal/tools/tools.go server/main_test.go
git commit -m "feat(tools): offline guard — every tool answers offline while stopped"
```

---

### Task 3: Swift Kit — `ConfigStore.offline` / `applyOffline`

**Files:**
- Modify: `app/Sources/ScreenReaderKit/ConfigStore.swift` (after `applyAccess`, ~line 101; doc comment line 6)
- Test: `app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift` (after `testApplyAccess_preservesOtherKeys`, ~line 75)

**Interfaces:**
- Consumes: nothing.
- Produces: `ConfigStore.offline(from: [String: Any]) -> Bool` (missing/wrong type → `false`) and `ConfigStore.applyOffline(_ offline: Bool, to: [String: Any]) -> [String: Any]` (preserves every other key) — Tasks 4-5 use exactly these names.

- [ ] **Step 1: Write the failing tests**

```swift
func testOfflineDefaultsFalse() {
    XCTAssertFalse(ConfigStore.offline(from: [:]))
    XCTAssertTrue(ConfigStore.offline(from: ["offline": true]))
    XCTAssertFalse(ConfigStore.offline(from: ["offline": "yes"])) // wrong type → default
}

func testApplyOffline_preservesOtherKeys() {
    let json: [String: Any] = [
        "app": "A",
        "access": "read_only",
        "capture": ["max_width": 1600],
    ]
    let out = ConfigStore.applyOffline(true, to: json)
    XCTAssertEqual(out["offline"] as? Bool, true)
    XCTAssertEqual(out["app"] as? String, "A")
    XCTAssertEqual(out["access"] as? String, "read_only")
    XCTAssertEqual((out["capture"] as? [String: Any])?["max_width"] as? Int, 1600)
    // round-trip
    XCTAssertEqual(ConfigStore.offline(from: ConfigStore.applyOffline(false, to: [:])), false)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter ConfigStoreTests 2>&1 | tail -5`
Expected: compile error `type 'ConfigStore' has no member 'offline'`.

- [ ] **Step 3: Implement**

In `ConfigStore.swift`, after `applyAccess` (line 101):

```swift
    /// Agent kill switch (config "offline"): true = the app stopped the server
    /// and every tool call is answered "offline". Missing/false = running.
    /// App-owned like access — the Go guard only reads it.
    public static func offline(from json: [String: Any]) -> Bool {
        json["offline"] as? Bool ?? false
    }

    /// Sets offline only; preserves every other key.
    public static func applyOffline(_ offline: Bool, to json: [String: Any]) -> [String: Any] {
        var out = json
        out["offline"] = offline
        return out
    }
```

And extend the doc comment on line 6 to: `/// Mutates only app/title/mode/screen/rect/camera/http/access/hotkey/offline; preserves every other key.`

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift build && swift test` → PASS (2 new + all pre-existing).

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/ConfigStore.swift app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift
git commit -m "feat(app): ConfigStore offline get/apply"
```

---

### Task 4: Swift app — one stop/start choke point (menu, F9, timer, launch reset)

**Files:**
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (hotkey wiring line 44; launch block lines 40-83; `toggleAutoStop`/`toggleServer` lines 92-94/159-163; `tickMenu` lines 145-151; `updateMenu` lines 246-299)

**Interfaces:**
- Consumes: `ConfigStore.offline(from:)` / `applyOffline(_:to:)` from Task 3; `ServerProcess` unchanged (`start()`, `stop()` for quit only, `state.isUp`).
- Produces: private `performStop()`, `performStart()`, `writeOffline(_:) -> Bool` on `AppDelegate`, and the menu's dispatch rule (up && online → stop action; anything else → start action). Task 5 wires the settings window to the same dispatch. UI rules follow spec §4.3 (dot badge + action item + timer row = "can do real work" = up **and** online).

- [ ] **Step 1: Route F9 through the pause action**

Line 44: replace

```swift
        hotkeys = HotkeyCenter { [weak self] in self?.server.stop() }
```

with

```swift
        hotkeys = HotkeyCenter { [weak self] in self?.performStop() }
```

and adjust the comment above it (lines 42-43) to: `// The stop key is live from the first moment the app is up; performStop() is deduped (it only ever stops), so firing it while paused or down does nothing.` (Safe on threading: `HotkeyCenter` already hops to the main queue before firing, so `performStop`'s main-thread read of `server.state` holds.)

- [ ] **Step 2: Add the choke point next to `toggleServer` (replacing it)**

Replace `@objc private func toggleServer() { ... }` (lines 159-163, including its comment and the old `scheduleAutoStop`-before-toggle logic) with:

```swift
    /// The single stop/start entry — menu item, F9 hotkey, 4 h timer and the
    /// settings window button all land here. "Stop" NEVER kills the process:
    /// the server runs iff the app runs (spec §2), stopping only sets the
    /// config "offline" flag, which the Go guard applies to every tool call.
    @objc private func toggleServer() {
        let json = ((try? ConfigStore.load()) ?? [:])
        if server.state.isUp && !ConfigStore.offline(from: json) {
            performStop()
        } else {
            performStart()
        }
    }

    /// The stop lever: pauses the agent via the flag. No-op when already
    /// paused or the process is down — like the old hotkey, it only ever
    /// stops, so it can never be the reason a server is running.
    private func performStop() {
        let json = ((try? ConfigStore.load()) ?? [:])
        guard server.state.isUp && !ConfigStore.offline(from: json) else { return }
        guard writeOffline(true) else { updateMenu(); return } // failed write: stay online, don't half-stop
        cancelAutoStop() // nothing left to auto-stop; the start lever opens a fresh window
        updateMenu()
    }

    /// The start lever: unpause, spawn the process when it is down, and open
    /// a fresh 4 h window either way — a resume without a deadline would
    /// never cap real work again.
    private func performStart() {
        guard writeOffline(false) else { updateMenu(); return }
        if !server.state.isUp { server.start() }
        scheduleAutoStop()
        updateMenu()
    }

    /// Writes the offline flag (no-op writes are skipped); on failure ALERTS
    /// and changes nothing — the same leave-config-alone contract as the
    /// access toggle — and returns false so callers abort the action.
    @discardableResult
    private func writeOffline(_ offline: Bool) -> Bool {
        do {
            var json = try ConfigStore.load()
            guard ConfigStore.offline(from: json) != offline else { return true }
            json = ConfigStore.applyOffline(offline, to: json)
            try ConfigStore.save(json)
            return true
        } catch {
            alert("Config write failed",
                  "\(error)\nThe app will not overwrite config.json — fix it by hand.")
            return false
        }
    }
```

- [ ] **Step 3: 4 h timer fires the pause**

Replace `tickMenu()`'s body (lines 145-151):

```swift
    private func tickMenu() {
        if let at = autoStopAt, Date() >= at { performStop() }
        updateMenu()
    }
```

(Old body nil'ed `autoStopAt` then called `server.stop()`; `performStop()` → `cancelAutoStop()` now owns the nil-ing on success, and a failed config write keeps the deadline pending so the cap is not silently lost.)

- [ ] **Step 4: Launch resets to online (spec §2)**

In `applicationDidFinishLaunching`, directly **before** `server.start()` (line 79), insert:

```swift
        // The pause is runtime state — an app launch (re)boots online (spec §2).
        // A crash-respawn of the child while the app stays up is NOT a launch
        // and keeps the flag. A corrupt config is left untouched here (same
        // contract as ensureToken); the Go guard fails closed and says so.
        if let json = try? ConfigStore.load(), ConfigStore.offline(from: json) {
            do { try ConfigStore.save(ConfigStore.applyOffline(false, to: json)) }
            catch { NSLog("screenreader: could not reset offline flag: \(error)") }
        }
```

`applicationWillTerminate` keeps `server.stop()` unchanged (the one real kill — the app is going away).

- [ ] **Step 5: Menu displays offline (spec §4.3)**

In `updateMenu()` replace the block from `let up = server.state.isUp` through the `serverLine.title = "Server: " ...` closing `}()` with:

```swift
        // "Can do real work" = process up AND not paused. The dot badge, the
        // action item and the 4 h timer row all key on that (spec §4.3).
        let offline = ConfigStore.offline(from: json)
        let online = server.state.isUp && !offline
        // The registered stop key is shown on the stop item only while the
        // action it performs is "stop" (paused/down shows nothing).
        let hk = online ? (Hotkey.parse(ConfigStore.hotkey(from: json)).map { " (\(Hotkey.glyphs($0)))" } ?? "") : ""
        serverActionItem.title = (online ? "Server: stop" : "Server: start") + hk
        if menuIconShownBadged != online {
            menuIconShownBadged = online
            statusItem.button?.image = online ? menuIconActive : menuIconIdle
        }
        // The 4 h timer only makes sense while the agent can do real work —
        // the item disappears when the server is down OR paused. isHidden on
        // the fixed item keeps the flicker-free no-teardown rule.
        autoStopItem.isHidden = !online
        if let at = autoStopAt {
            // "server stop in …" is the 4h timer's CURRENT value (time left).
            autoStopItem.title = "Timer: clear (server stop in \(TimerClock.format(at.timeIntervalSinceNow)))"
        } else {
            autoStopItem.title = "Timer: start"
        }
        // The agent's current access mode, retitled from the config on every
        // refresh (menu open, 30 s tick, after a toggle or a settings edit).
        accessItem.title = AccessToggle.title(ConfigStore.access(from: json))
        serverLine.title = "Server: " + {
            switch server.state {
            case .running:
                return offline
                    ? "offline — stopped (still listening on \(addr))"
                    : "running on \(addr)"
            case .starting, .restarting: return "starting…"
            case .crashed: return "CRASHED — check the log"
            case .stopped: return "not running"
            }
        }()
```

The `onStateChange` closure (lines 67-78) stays keyed on `s.isUp` for the tick/auto-stop bookkeeping — menu tick while paused is harmless (cheap, keeps titles fresh).

- [ ] **Step 6: Build + full test pass**

Run: `cd app && swift build && swift test` → PASS.
Also: `cd server && CGO_ENABLED=1 go build .` (untouched, catches nothing new — skip if no server file changed).

- [ ] **Step 7: Commit**

```bash
git add app/Sources/ScreenReader/AppDelegate.swift
git commit -m "feat(app): menu, F9 and 4h timer pause via the offline flag"
```

---

### Task 5: Swift app — settings window routes through the choke point

**Files:**
- Modify: `app/Sources/ScreenReader/SettingsWindow.swift` (properties ~line 6-11 and init ~line 40-45; server section refresh ~lines 263-271; `toggleServer` ~line 630)
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (`openSettings`, lines 172-184 — pass the new callback)

**Interfaces:**
- Consumes: Task 4's dispatch rule (the settings button must do exactly what the menu item does).
- Produces: `SettingsWindowController.init(server:hotkeyChanged:hotkeySuspended:serverAction:)` — the new final parameter is `serverAction: @escaping () -> Void`.

- [ ] **Step 1: Add the callback to the controller**

In the property block after `hotkeySuspended` (~line 11) add:

```swift
    /// Runs the app's single stop/start action (same dispatch as the menu
    /// item — pause via the offline flag, never a kill).
    private let serverAction: () -> Void
```

Extend `init` (lines 40-45) to:

```swift
    init(server: ServerProcess, hotkeyChanged: @escaping (String) -> Void,
         hotkeySuspended: @escaping (Bool) -> Void,
         serverAction: @escaping () -> Void) {
        self.server = server
        self.hotkeyChanged = hotkeyChanged
        self.hotkeySuspended = hotkeySuspended
        self.serverAction = serverAction
```

Replace the old action (line ~630):

```swift
    @objc private func toggleServer() { serverAction() }
```

- [ ] **Step 2: Relabel from up AND offline**

In `refresh()`, `let json` already exists; after it add `let offline = ConfigStore.offline(from: json)`, then replace the whole `switch server.state { ... }` block and the `serverButton.title` line (spec §4.3 again):

```swift
        switch server.state {
        case .running:               serverLabel.stringValue = offline
                                        ? "offline — stopped (still listening on \(addr))"
                                        : "running on \(addr)"
        case .starting, .restarting: serverLabel.stringValue = "starting…"
        case .crashed:               serverLabel.stringValue = "crashed"
        case .stopped:               serverLabel.stringValue = "stopped"
        }
        serverButton.title = (server.state.isUp && !offline) ? "Stop server" : "Start server"
```

- [ ] **Step 3: Pass the callback from `openSettings` (AppDelegate, lines 175-180)**

```swift
            settingsWC = SettingsWindowController(
                server: server,
                hotkeyChanged: { [weak self] spec in self?.applyHotkeySpec(spec) },
                hotkeySuspended: { [weak self] s in self?.hotkeys.setSuspended(s) },
                serverAction: { [weak self] in self?.toggleServer() })
```

(`toggleServer` is the Task 4 dispatcher — one choke point for all four entry paths, spec §4.2.)

- [ ] **Step 4: Build + test**

Run: `cd app && swift build && swift test` → PASS.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReader/SettingsWindow.swift app/Sources/ScreenReader/AppDelegate.swift
git commit -m "feat(app): settings server button shares the pause choke point"
```

---

### Task 6: Docs, version bump, live verification

**Files:**
- Modify: `docs/reference.md` (TOC ~line 15; config sample ~line 86; key docs ~line 102; camera note ~line 72; App details ~lines 194-195; Stop hotkey ~lines 203-228; new section before `## Tools`; Safety bullet ~line 272; Troubleshooting rows)
- Modify: `AGENTS.md` (hotkey bullet; app-mutated-keys list)
- Modify: `app/Info.plist:11` (version)

**Interfaces:**
- Consumes: everything above; exact flag name `offline`, exact status string (Global Constraints), lifecycle invariant from spec §2.
- Produces: shipped docs + `1.4.0`; live-verified feature.

- [ ] **Step 1: reference.md — TOC + config sample + key docs**

TOC: after `- [Stop hotkey](#stop-hotkey)` insert `- [Offline mode](#offline-mode)`.

Config sample (the JSON block under "Edit the config file"): add the line `  "offline": false,` after `"access": "full",`.

After the `hotkey` key paragraph (the one ending `…cannot drop it.`) insert:

```markdown
`offline` is the app's stop switch: while `true`, the still-running server refuses **every** tool call with `offline — server stopped from the Screen Reader app …` — see [Offline mode](#offline-mode). The menu-bar app writes it; the `access`/`hotkey` rules still apply after resuming.
```

- [ ] **Step 2: reference.md — camera note, App details bullets**

In the camera permissions paragraph, replace `the cached session tears down 30 s after the last frame, and stopping the
server releases the camera immediately.` with `the cached session tears down 30 s after the last frame, and stopping releases the camera immediately: while the server is offline every refused call closes the session, so the light is off within 30 s even if nothing calls again.`

In **App details**: replace the bullet `- Quitting the app stops the MCP endpoint (start the app again to serve).` with:

```markdown
- The server process runs **iff the app runs**: stopping (menu **Server: stop** or the hotkey) only *pauses* it into [Offline mode](#offline-mode) — the MCP connection survives; quitting the app is what ends the endpoint (reopen the app to serve again).
```

and extend the keys bullet to list `…/`access`/`hotkey`/`offline``.

- [ ] **Step 3: reference.md — Stop hotkey section + new Offline mode section**

In **Stop hotkey**: first sentence `…global hotkey that stops the MCP server:` → `…global hotkey that stops the agent (`[Offline mode](#offline-mode)`)`; and in the "It only ever **stops**" bullet, after `Pressing it while the server is down does nothing.` append ` It never kills the process and never drops the MCP connection.`

Insert this new section immediately before `## Tools`:

```markdown
## Offline mode

The app's **Server: stop** (and the stop hotkey / the 4 h timer) does not kill
the Go process — it flips `"offline": true` in the shared config, and the
still-running, still-connected server refuses **every** tool call with:

> `offline — server stopped from the Screen Reader app (start it from the menu bar item, or set "offline": false in ~/.config/screenreader/config.json)`

- **The MCP connection stays healthy** while offline: no failed-connection noise
  in the client; the tool list is unchanged; the refusal is a normal tool error.
- Re-read on every call (like `access`), so stop and resume apply mid-session
  with no restart. Bare stdio/HTTP users can toggle the key by hand.
- Fails **closed**: an unparseable config also refuses calls (naming the config
  error) rather than silently resuming real clicks.
- While offline, every refused call closes the cached camera session, so the
  indicator light goes off within 30 s even if no further calls arrive.
- **Start** from the menu/settings unpause-and-caps afresh (4 h window); an app
  launch always boots online. Quitting the app still ends the process.
- `access` is orthogonal: `read_only` decides *which* tools exist, offline
  suspends *all* of them until resumed.
```

- [ ] **Step 4: reference.md — Safety + Troubleshooting**

Safety bullet 1: `Press the **stop hotkey** (default **F9**) to kill the server instantly from any app — see [Stop hotkey](#stop-hotkey).` → `Press the **stop hotkey** (default **F9**) to pause the agent instantly from any app — every tool answers `offline`; see [Offline mode](#offline-mode).`

Troubleshooting table:
- Row `| capture hangs with no result while /health still answers |` — replace `restart the server from the menu bar app (or `kill` the `screenreader --http` child — the app respawns it)` with `restart the process (quit and reopen the app, or `kill` the `screenreader --http` child — the app respawns it)`.
- Add a row: `| All tools answer `offline — server stopped …` | The app paused the server (menu **Server: stop** or the stop hotkey). Resume with **Server: start**, or set `"offline": false` in the config. |`

- [ ] **Step 5: AGENTS.md updates**

- App-mutated keys sentence: `The app mutates only `app`/`title`/`mode`/`rect`/`camera`/`http`/`hotkey`/`access` in config.json` → append `/`offline``.
- Stop-hotkey bullet: replace `Fire → `ServerProcess.stop()` — the single stop choke point, deduped; never hook `killChild()`, which also serves restarts.` with `Fire → `AppDelegate.performStop()` — the single stop choke point (also the menu item, the 4h timer and the settings button): it pauses via the config `offline` flag (Go's per-call `offlineGuard` refuses every tool and `camera.Close()`s the session), deduped, and never kills the process — the server runs iff the app runs; `performStart()` unpauses (and spawns if the process is down); an app launch resets the flag to online. App quit still kills the child (`applicationWillTerminate` → `ServerProcess.stop()`).`
- Camera bullet tail: replace `the F9 stop hotkey kills the server, which tears the session down and turns the light off.` with `F9 pauses instead of killing: the offline guard `camera.Close()`s the session on every refused call and the idle teardown bounds it at 30 s.`

- [ ] **Step 6: Version bump**

`app/Info.plist` line 11: `<key>CFBundleShortVersionString</key><string>1.3.0</string>` → `1.4.0`.

- [ ] **Step 7: Full automated gate**

```bash
cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...
cd ../app && swift build && swift test
cd .. && ./build.sh
```

Expected: all green, `app/build/Screen Reader MCP.app` assembled and signed.

- [ ] **Step 8: Live verification (manual — needs granted app + connected opencode)**

1. Relaunch the built app; opencode stays connected.
2. Menu → **Server: stop (F9)**: menu line shows `offline — stopped (still listening on …)`, dot badge off, Timer row hidden; any screenreader tool in opencode answers the exact `offline — …` string; MCP stays green (no reconnect).
3. Press **F9** while online → same; press again while paused → nothing changes.
4. Menu → **Server: start**: tools work immediately, dot returns, fresh `Timer: clear (…)` line.
5. With a camera target: stop → indicator off within 30 s.
6. Kill the Go child (`kill <pid>`): app respawns it; if paused it comes back **paused** (menu still says offline).
7. Quit the app → connection drops (app closed = server gone).

- [ ] **Step 9: Commit**

```bash
git add docs/reference.md AGENTS.md app/Info.plist
git commit -m "docs: offline mode reference + agent notes; app 1.4.0"
```
