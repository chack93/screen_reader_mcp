import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()

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
        rebuild()
        server.onStateChange = { [weak self] _ in self?.rebuild() }
        server.start()
    }

    func applicationWillTerminate(_ notification: Notification) { server.stop() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func menuWillOpen(_ menu: NSMenu) { rebuild() }

    @objc private func restartServer() { server.restart() }

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

    /// Rebuild the status menu: live server/target/permission lines + actions.
    private func rebuild() {
        guard let menu = statusItem.menu else { return }
        menu.removeAllItems()
        let json = ((try? ConfigStore.load()) ?? [:])
        let (addr, _) = ConfigStore.httpConfig(from: json)

        func line(_ s: String) {
            let i = NSMenuItem(title: s, action: nil, keyEquivalent: "")
            i.isEnabled = false
            menu.addItem(i)
        }
        line("Server: " + {
            switch server.state {
            case .running: return "running on \(addr)"
            case .starting, .restarting: return "starting…"
            case .crashed: return "CRASHED — check the log"
            case .stopped: return "not running"
            }
        }())
        let t = ConfigStore.target(from: json)
        if t.mode == "rect", t.w > 0 {
            line("Target: rectangle (\(t.x), \(t.y), \(t.w), \(t.h))")
        } else if !t.app.isEmpty {
            line("Target: \(t.app)" + (t.title.isEmpty ? "" : " — \(t.title)"))
        } else {
            line("Target: not set")
        }
        for p in TCCPermission.allCases {
            line("\(p.rawValue): " + (p.isGranted ? "✓" : "✗"))
        }
        menu.addItem(.separator())
        for (label, sel) in [("Restart server", #selector(restartServer)),
                             ("Test capture…", #selector(testCapture))] {
            let i = menu.addItem(withTitle: label, action: sel, keyEquivalent: "")
            i.target = self
        }
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
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
