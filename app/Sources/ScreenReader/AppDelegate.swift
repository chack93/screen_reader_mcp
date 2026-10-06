import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()

    func applicationDidFinishLaunching(_ notification: Notification) {
        ensureToken()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.title = "SR"
        let menu = NSMenu()
        let restart = menu.addItem(withTitle: "Restart server",
                                   action: #selector(restartServer), keyEquivalent: "")
        restart.target = self
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        statusItem.menu = menu
        server.start()
    }

    func applicationWillTerminate(_ notification: Notification) { server.stop() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

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
}
