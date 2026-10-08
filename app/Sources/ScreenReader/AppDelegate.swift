import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()
    private var settingsWC: SettingsWindowController?

    /// The menu has a fixed structure, built once. These references let
    /// updateMenu() refresh the dynamic lines in place instead of
    /// removeAllItems + re-add — tearing the menu down on every state change
    /// made the open menu flicker (and left a ghost-duplicated section on the
    /// first open after launch).
    private var serverLine: NSMenuItem!
    private var targetLine: NSMenuItem!
    private var permLines: [NSMenuItem] = []

    /// Cached permission state, refreshed in the background. The .automation
    /// probe is a blocking osascript (~100–300 ms) and must never run on the
    /// main thread while the menu is being shown.
    private var permCache: [TCCPermission: Bool] = [:]
    private var permRefreshInFlight = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        ensureToken()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let img = NSImage(systemSymbolName: "menubar.rectangle",
                             accessibilityDescription: "ScreenReader") {
            statusItem.button?.image = img
        } else {
            statusItem.button?.title = "SR"
        }
        let menu = NSMenu()
        menu.delegate = self
        statusItem.menu = menu
        buildMenu(menu)
        updateMenu()
        refreshPermissions()
        server.onStateChange = { [weak self] _ in self?.updateMenu() }
        server.start()
    }

    func applicationWillTerminate(_ notification: Notification) { server.stop() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func menuWillOpen(_ menu: NSMenu) {
        updateMenu()
        refreshPermissions()
    }

    @objc private func restartServer() { server.restart() }

    @objc private func openSettings() {
        // T16 fix: replace a controller whose window was closed, so the old
        // one (and its now-invalidated timer) deallocates; reuse a visible one.
        if settingsWC == nil || settingsWC?.window == nil || !(settingsWC?.window?.isVisible ?? false) {
            settingsWC = SettingsWindowController(server: server)
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
        for (label, sel) in [("Open ScreenReader…", #selector(openSettings)),
                              ("Restart server", #selector(restartServer)),
                              ("Test capture…", #selector(testCapture))] {
            let i = menu.addItem(withTitle: label, action: sel, keyEquivalent: "")
            i.target = self
        }
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
    }

    /// Refresh the dynamic menu lines in place (title mutations only) — safe
    /// even while the menu is open, unlike removeAllItems + re-add.
    private func updateMenu() {
        guard serverLine != nil else { return }
        let json = ((try? ConfigStore.load()) ?? [:])
        let (addr, _) = ConfigStore.httpConfig(from: json)
        serverLine.title = "Server: " + {
            switch server.state {
            case .running: return "running on \(addr)"
            case .starting, .restarting: return "starting…"
            case .crashed: return "CRASHED — check the log"
            case .stopped: return "not running"
            }
        }()
        let t = ConfigStore.target(from: json)
        if t.mode == "screen" {
            targetLine.title = "Target: screen \(t.screen + 1)"
        } else if t.mode == "rect", t.w > 0 {
            targetLine.title = "Target: rectangle (\(t.x), \(t.y), \(t.w), \(t.h))"
        } else if !t.app.isEmpty {
            targetLine.title = "Target: \(t.app)" + (t.title.isEmpty ? "" : " — \(t.title)")
        } else {
            targetLine.title = "Target: not set"
        }
        for (p, i) in zip(TCCPermission.allCases, permLines) {
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
                      "Privacy & Security → Screen Recording shows ScreenReader ITSELF (not your " +
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
