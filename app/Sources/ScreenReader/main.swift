import AppKit

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory) // menu-bar only, no dock icon
if CommandLine.arguments.contains("--smoke") {
    // Build-verification mode: shows the item briefly, exits cleanly.
    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { NSApp.terminate(nil) }
}
app.run()
