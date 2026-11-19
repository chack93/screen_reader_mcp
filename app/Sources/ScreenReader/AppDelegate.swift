import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()
    private var settingsWC: SettingsWindowController?
    /// The global "stop the server" key (config `hotkey`), registered via Carbon.
    private var hotkeys: HotkeyCenter!

    /// The menu has a fixed structure, built once. These references let
    /// updateMenu() refresh the dynamic lines in place instead of
    /// removeAllItems + re-add — tearing the menu down on every state change
    /// made the open menu flicker (and left a ghost-duplicated section on the
    /// first open after launch).
    private var serverLine: NSMenuItem!
    private var serverActionItem: NSMenuItem!
    private var autoStopItem: NSMenuItem!
    private var accessItem: NSMenuItem!
    /// Deadline of the pending 4h auto-stop (nil = timer off).
    private var autoStopAt: Date?
    /// Prebuilt menu bar icons: plain (server down) and dot-badged (server up).
    private var menuIconIdle: NSImage?
    private var menuIconActive: NSImage?
    /// Whether the badge variant is currently shown — only swap the image
    /// on an actual up/down flip.
    private var menuIconShownBadged: Bool?
    /// 30 s tick, alive only while the server is up: keeps the timer title
    /// fresh and performs the 4h auto-pause at its deadline.
    private var menuTick: Timer?
    private var targetLine: NSMenuItem!
    private var permLines: [NSMenuItem] = []

    /// Cached permission state, refreshed in the background. The .automation
    /// probe is a blocking osascript (~100–300 ms) and must never run on the
    /// main thread while the menu is being shown.
    private var permCache: [TCCPermission: Bool] = [:]
    private var permRefreshInFlight = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        ensureToken()
        // The stop key is live from the first moment the app is up; performStop() is
        // deduped (it only ever stops), so firing it while paused or down does nothing.
        hotkeys = HotkeyCenter { [weak self] in self?.performStop() }
        applyHotkeySpec(ConfigStore.hotkey(from: (try? ConfigStore.load()) ?? [:]))
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        // The dot badge indicates a RUNNING server. The dot diameter scales
        // with the symbol's pixel size so it stays proportional at @2x.
        if let img = NSImage(systemSymbolName: "menubar.rectangle",
                             accessibilityDescription: "Screen Reader MCP"),
           let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) {
            menuIconIdle = NSImage(cgImage: cg, size: img.size)
            menuIconIdle?.isTemplate = true
            let dot = CGFloat(cg.width) * 0.22
            menuIconActive = NSImage(cgImage: MenuBadge.withDot(cg, dotDiameter: dot, inset: dot * 0.25), size: img.size)
            menuIconActive?.isTemplate = true
            statusItem.button?.image = menuIconIdle
        } else {
            statusItem.button?.title = "SR"
        }
        let menu = NSMenu()
        menu.delegate = self
        statusItem.menu = menu
        buildMenu(menu)
        updateMenu()
        refreshPermissions()
        server.onStateChange = { [weak self] s in
            if s.isUp {
                self?.startMenuTick()
            } else {
                self?.stopMenuTick()
                // The 4h auto-stop exists to stop a RUNNING server; once the
                // server goes down (stopped or crashed) there is nothing left
                // to stop, so the pending deadline is dropped with it.
                self?.cancelAutoStop()
            }
            self?.updateMenu()
        }
        // The pause is runtime state — an app launch (re)boots online (spec §2).
        // A crash-respawn of the child while the app stays up is NOT a launch
        // and keeps the flag. A corrupt config is left untouched here (same
        // contract as ensureToken); the Go guard fails closed and says so.
        if let json = try? ConfigStore.load(), ConfigStore.offline(from: json) {
            do { try ConfigStore.save(ConfigStore.applyOffline(false, to: json)) }
            catch { NSLog("screenreader: could not reset offline flag: \(error)") }
        }
        server.start()
        // Opt-out, not opt-in: the 4h stop is pending from the moment the app
        // (and its server) start; the menu item cancels it.
        scheduleAutoStop()
    }

    func applicationWillTerminate(_ notification: Notification) {
        stopMenuTick()
        server.stop()
    }

    /// The 4h auto-stop toggle: "Timer: start" when idle (click schedules),
    /// "Timer: clear (server pause in H:MM)" when pending (click cancels).
    @objc private func toggleAutoStop() {
        if autoStopAt == nil { scheduleAutoStop() } else { cancelAutoStop() }
    }

    /// Schedule the 4h deadline (no-op when already pending). Called at app
    /// start and on every explicit server start, so crash-backoff respawns
    /// keep the ORIGINAL deadline instead of extending it.
    private func scheduleAutoStop() {
        guard autoStopAt == nil else { return }
        autoStopAt = Date().addingTimeInterval(4 * 3600)
        updateMenu()
    }

    /// Cancel a pending 4h deadline (no-op when none) — the opt-out.
    private func cancelAutoStop() {
        guard autoStopAt != nil else { return }
        autoStopAt = nil
        updateMenu()
    }

    /// Flips the agent access mode in config.json (`access`). The Go server
    /// re-reads the config on every tool call and rebuilds its tool surface per
    /// HTTP request, so the change takes effect on the agent's next call — no
    /// restart, no relaunch. A failed write leaves the config untouched (same
    /// contract as the settings window) and says so.
    @objc private func toggleAccess() {
        do {
            var json = try ConfigStore.load()
            json = ConfigStore.applyAccess(
                AccessToggle.next(ConfigStore.access(from: json)), to: json)
            try ConfigStore.save(json)
        } catch {
            alert("Config write failed",
                  "\(error)\nThe app will not overwrite config.json — fix it by hand.")
        }
        updateMenu()
    }

    private func startMenuTick() {
        guard menuTick == nil else { return }
        let t = Timer(timeInterval: 30, repeats: true) { [weak self] _ in self?.tickMenu() }
        RunLoop.main.add(t, forMode: .common)
        menuTick = t
    }

    private func stopMenuTick() {
        menuTick?.invalidate()
        menuTick = nil
    }

    /// 30 s tick: performs the 4h auto-pause at its deadline and refreshes
    /// the menu titles (the timer value is otherwise only re-rendered
    /// when the menu opens or a state change lands).
    private func tickMenu() {
        if let at = autoStopAt, Date() >= at { performStop() }
        updateMenu()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func menuWillOpen(_ menu: NSMenu) {
        updateMenu()
        refreshPermissions()
    }

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

    /// Registers the configured stop hotkey ("off" / unparseable = nothing is
    /// registered) and refreshes the menu, which shows the key on the stop item.
    private func applyHotkeySpec(_ spec: String) {
        hotkeys?.update(Hotkey.parse(spec))
        updateMenu()
    }

    @objc private func openSettings() {
        // T16 fix: replace a controller whose window was closed, so the old
        // one (and its now-invalidated timer) deallocates; reuse a visible one.
        if settingsWC == nil || settingsWC?.window == nil || !(settingsWC?.window?.isVisible ?? false) {
            settingsWC = SettingsWindowController(
                server: server,
                hotkeyChanged: { [weak self] spec in self?.applyHotkeySpec(spec) },
                hotkeySuspended: { [weak self] s in self?.hotkeys.setSuspended(s) },
                serverAction: { [weak self] in self?.toggleServer() })
        }
        settingsWC?.showWindow(nil)
        settingsWC?.window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    /// First run: give the config an http section with a bearer token.
    /// A malformed config is left untouched (the server logs its own error).
    private func ensureToken() {
        do {
            var json = try ConfigStore.load()
            if ConfigStore.httpConfig(from: json).token.isEmpty {
                json = ConfigStore.ensureToken(ConfigStore.generateToken(), in: json)
                try ConfigStore.save(json)
            }
        } catch {
            NSLog("screenreader: config.json unreadable, leaving it alone: \(error)")
        }
    }

    /// Build the status menu's fixed structure once and keep references to the
    /// dynamic items. The item set never changes, so there is no need to tear
    /// the menu down on every refresh — doing so made the open menu flicker
    /// and left a ghost-duplicated section on the first open after launch.
    private func buildMenu(_ menu: NSMenu) {
        func labelLine() -> NSMenuItem {
            let i = NSMenuItem(title: "", action: nil, keyEquivalent: "")
            i.isEnabled = false
            menu.addItem(i)
            return i
        }
        serverLine = labelLine()
        targetLine = labelLine()
        permLines = TCCPermission.allCases.map { p in
            let i = labelLine()
            i.title = "\(p.rawValue): …"
            return i
        }
        menu.addItem(.separator())
        let open = menu.addItem(withTitle: "Open Screen Reader MCP…",
                                action: #selector(openSettings), keyEquivalent: "")
        open.target = self
        // Start/Stop is state-dependent: one fixed item, retitled in place by
        // updateMenu() — same flicker-free pattern as the status lines above.
        serverActionItem = menu.addItem(withTitle: "Server: start",
                                        action: #selector(toggleServer), keyEquivalent: "")
        serverActionItem.target = self
        autoStopItem = menu.addItem(withTitle: "Timer: start",
                                    action: #selector(toggleAutoStop), keyEquivalent: "")
        autoStopItem.target = self
        // Agent access: state-dependent like the two items above — one fixed
        // item retitled in place. Always available (it gates the agent's tool
        // surface, not the server), so unlike the timer it is never hidden.
        accessItem = menu.addItem(withTitle: "Access: full (switch to read-only)",
                                  action: #selector(toggleAccess), keyEquivalent: "")
        accessItem.target = self
        let test = menu.addItem(withTitle: "Test capture…",
                                action: #selector(testCapture), keyEquivalent: "")
        test.target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit Screen Reader MCP",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
    }

    /// Refresh the dynamic menu lines in place (title mutations only) — safe
    /// even while the menu is open, unlike removeAllItems + re-add.
    private func updateMenu() {
        guard serverLine != nil else { return }
        let json = ((try? ConfigStore.load()) ?? [:])
        let (addr, _) = ConfigStore.httpConfig(from: json)
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
            // "server pause in …" is the 4h timer's CURRENT value (time left).
            autoStopItem.title = "Timer: clear (server pause in \(TimerClock.format(at.timeIntervalSinceNow)))"
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
        let t = ConfigStore.target(from: json)
        if t.mode == "screen" {
            targetLine.title = "Target: screen \(t.screen + 1)"
        } else if t.mode == "camera" {
            targetLine.title = "Target: camera " + (t.cameraName.isEmpty ? "(system default)" : t.cameraName)
        } else if t.mode == "rect", t.w > 0 {
            targetLine.title = "Target: rectangle (\(t.x), \(t.y), \(t.w), \(t.h))"
        } else if !t.app.isEmpty {
            targetLine.title = "Target: \(t.app)" + (t.title.isEmpty ? "" : " — \(t.title)")
        } else {
            targetLine.title = "Target: not set"
        }
        for (p, i) in zip(TCCPermission.allCases, permLines) {
            i.isHidden = (p == .camera && t.mode != "camera") // fixed structure, hidden rows (autoStopItem precedent)
            i.title = "\(p.rawValue): " + (permCache[p].map { $0 ? "✓" : "✗" } ?? "…")
        }
    }

    /// Refresh the cached permission state off the main thread — the
    /// .automation probe is a blocking osascript (~100–300 ms). At most one
    /// probe flight at a time; results apply on main and redraw the lines.
    private func refreshPermissions() {
        guard !permRefreshInFlight else { return }
        permRefreshInFlight = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let results = Dictionary(uniqueKeysWithValues:
                TCCPermission.allCases.map { ($0, $0.isGranted) })
            DispatchQueue.main.async {
                guard let self else { return }
                self.permRefreshInFlight = false
                self.permCache = results
                self.updateMenu()
            }
        }
    }

    /// The spec §7 canary: run a real capture through the Go child and check
    /// that its pixels are not uniformly black (= the app's Screen Recording
    /// grant is in effect for the child process).
    @objc private func testCapture() {
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        guard let url = URL(string: "http://\(addr)") else { return }
        let client = MCPClient(baseURL: url, token: token.isEmpty ? nil : token)
        Task { @MainActor in
            let result: [String: Any]
            do { result = try await client.callTool("capture") }
            catch { alert("Test capture failed", "\(error)"); return }

            if result["isError"] as? Bool == true {
                alert("Test capture failed", textContent(result) ?? "tool error")
                return
            }
            guard let content = result["content"] as? [[String: Any]],
                  let img = content.first(where: { $0["type"] as? String == "image" }),
                  let b64 = img["data"] as? String,
                  let png = Data(base64Encoded: b64) else {
                alert("Test capture failed", textContent(result) ?? "capture returned no image")
                return
            }
            if CaptureCheck.isAllBlack(png) {
                alert("Test capture: ALL BLACK",
                      "Screen Recording is not effective for the app. Check System Settings → " +
                      "Privacy & Security → Screen Recording shows Screen Reader MCP ITSELF (not your " +
                      "terminal), then quit and reopen the app. If it is still black, STOP — the " +
                      "child-process attribution assumption (§7) failed and must be escalated.")
            } else {
                alert("Test capture OK",
                      "Non-black image — the Go server runs under the app's permissions.")
            }
        }
    }

    private func textContent(_ result: [String: Any]) -> String? {
        ((result["content"] as? [[String: Any]])?
            .compactMap { ($0["type"] as? String) == "text" ? ($0["text"] as? String) : nil })?
            .joined(separator: "\n")
    }

    private func alert(_ title: String, _ info: String) {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        a.runModal()
    }
}
