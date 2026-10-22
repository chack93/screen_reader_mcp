import AppKit
import ScreenReaderKit

final class SettingsWindowController: NSWindowController, NSWindowDelegate {
    private let server: ServerProcess
    private var permStatus: [TCCPermission: NSTextField] = [:]
    private var grantButton: NSButton!
    private var segmented: NSSegmentedControl!
    private var pickerRow: NSStackView!
    private var rectRow: NSStackView!
    private var screenRow: NSStackView!
    private var picker: NSPopUpButton!
    private var screenPicker: NSPopUpButton!
    private var rectLabel: NSTextField!
    private var serverLabel: NSTextField!
    private var serverButton: NSButton!
    private var snippetView: NSTextView!
    private var stderrView: NSTextView!
    private var accessSegmented: NSSegmentedControl!
    private var accessCaption: NSTextField!
    private var lastAccessKey = ""
    private var lastModeKey = ""
    private var lastScreensFingerprint = ""
    private var timer: Timer?

    init(server: ServerProcess) {
        self.server = server
        let win = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 715),
                           styleMask: [.titled, .closable], backing: .buffered, defer: false)
        win.title = "Screen Reader MCP"
        win.center()
        super.init(window: win)
        win.delegate = self   // T16 fix: stop the refresh timer when the window closes
        build()
        refresh()
        // Populate the App window picker on open if it's still empty, so the
        // user isn't forced to hit Refresh before seeing any windows.
        if picker.itemArray.isEmpty { refreshPicker() }
        let t = Timer(timeInterval: 2, repeats: true) { [weak self] _ in self?.refresh() }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }

    required init?(coder: NSCoder) { fatalError("not used") }
    deinit { timer?.invalidate() }   // belt-and-braces

    /// The 2 s refresh timer must NOT outlive the window: every tick runs the
    /// .automation probe (a blocking osascript + waitUntilExit, ~100–300 ms) on
    /// the main thread. Without this, closing the window leaves that stall
    /// running for the app's entire life (settingsWC is cached in AppDelegate).
    func windowWillClose(_ notification: Notification) { timer?.invalidate() }

    // MARK: - Build

    private func build() {
        let root = stack(.vertical)
        root.spacing = 14
        root.alignment = .leading

        // --- Permissions ---
        let perms = stack(.vertical)
        perms.addArrangedSubview(sectionTitle("Permissions"))
        for (idx, p) in TCCPermission.allCases.enumerated() {
            let row = stack(.horizontal)
            let status = NSTextField(labelWithString: "…")
            let btn = NSButton(title: "Request", target: self, action: #selector(requestPerm(_:)))
            btn.tag = idx
            row.addArrangedSubview(NSTextField(labelWithString: p.rawValue))
            row.addArrangedSubview(NSView()) // spacer
            row.addArrangedSubview(status)
            row.addArrangedSubview(btn)
            row.widthAnchor.constraint(equalToConstant: 420).isActive = true
            permStatus[p] = status
            perms.addArrangedSubview(row)
        }
        grantButton = NSButton(title: "Grant access", target: self, action: #selector(grantAll))
        perms.addArrangedSubview(grantButton)
        root.addArrangedSubview(perms)

        // --- Target ---
        let target = stack(.vertical)
        target.alignment = .centerX
        // The section must keep a CONSTANT width in every mode: an NSStackView
        // otherwise sizes to its widest VISIBLE child, so switching to
        // Rectangle (narrow row only) collapsed the section and the leading
        // root pinned it left, breaking the centering. 420 matches the other
        // sections' fixed row widths.
        target.widthAnchor.constraint(equalToConstant: 420).isActive = true
        target.addArrangedSubview(sectionTitle("Target"))
        segmented = NSSegmentedControl(labels: ["App window", "Rectangle", "Screen"],
                                       trackingMode: .selectOne, target: self,
                                       action: #selector(modeChanged))
        target.addArrangedSubview(segmented)

        pickerRow = stack(.horizontal)
        picker = NSPopUpButton(frame: .zero, pullsDown: false)
        picker.target = self
        picker.action = #selector(pickerChanged)
        picker.widthAnchor.constraint(equalToConstant: 320).isActive = true
        pickerRow.addArrangedSubview(picker)
        pickerRow.addArrangedSubview(NSButton(title: "Refresh", target: self,
                                              action: #selector(refreshPicker)))

        rectRow = stack(.horizontal)
        rectRow.addArrangedSubview(NSButton(title: "Select rectangle…", target: self,
                                            action: #selector(selectRect)))
        rectLabel = NSTextField(labelWithString: "")
        rectLabel.font = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
        rectRow.addArrangedSubview(rectLabel)

        screenRow = stack(.horizontal)
        screenPicker = NSPopUpButton(frame: .zero, pullsDown: false)
        screenPicker.target = self
        screenPicker.action = #selector(screenPickerChanged)
        screenPicker.widthAnchor.constraint(equalToConstant: 320).isActive = true
        screenRow.addArrangedSubview(screenPicker)
        screenRow.addArrangedSubview(NSButton(title: "Refresh", target: self,
                                              action: #selector(refreshScreenPicker)))

        target.addArrangedSubview(pickerRow)
        target.addArrangedSubview(rectRow)
        target.addArrangedSubview(screenRow)
        root.addArrangedSubview(target)

        // --- Agent access ---
        let accessSection = stack(.vertical)
        accessSection.addArrangedSubview(sectionTitle("Agent access"))
        accessSegmented = NSSegmentedControl(labels: ["Full access", "Read-only"],
                                             trackingMode: .selectOne, target: self,
                                             action: #selector(accessChanged))
        accessSection.addArrangedSubview(accessSegmented)
        accessCaption = NSTextField(wrappingLabelWithString: "")
        accessCaption.font = NSFont.systemFont(ofSize: 11)
        accessCaption.textColor = .secondaryLabelColor
        accessCaption.widthAnchor.constraint(equalToConstant: 420).isActive = true
        accessSection.addArrangedSubview(accessCaption)
        root.addArrangedSubview(accessSection)

        // --- Server ---
        let srv = stack(.vertical)
        srv.addArrangedSubview(sectionTitle("Server"))
        let srvRow = stack(.horizontal)
        serverLabel = NSTextField(labelWithString: "…")
        srvRow.addArrangedSubview(serverLabel)
        srvRow.addArrangedSubview(NSView())
        // State-dependent: retitled Start/Stop by refresh(), which runs at
        // build time and every 2 s.
        serverButton = NSButton(title: "Start server", target: self,
                                action: #selector(toggleServer))
        srvRow.addArrangedSubview(serverButton)
        srvRow.addArrangedSubview(NSButton(title: "Copy opencode config", target: self,
                                           action: #selector(copySnippet)))
        srvRow.widthAnchor.constraint(equalToConstant: 420).isActive = true
        srv.addArrangedSubview(srvRow)
        snippetView = readOnlyTextView(height: 110)
        srv.addArrangedSubview(snippetView)
        stderrView = readOnlyTextView(height: 50)
        stderrView.isHidden = true
        srv.addArrangedSubview(stderrView)
        root.addArrangedSubview(srv)

        let content = NSView()
        content.addSubview(root)
        NSLayoutConstraint.activate([
            root.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 16),
            root.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -16),
            root.topAnchor.constraint(equalTo: content.topAnchor, constant: 16),
        ])
        window?.contentView = content
    }

    private func stack(_ o: NSUserInterfaceLayoutOrientation) -> NSStackView {
        let s = NSStackView(); s.orientation = o; s.spacing = 6
        s.translatesAutoresizingMaskIntoConstraints = false
        return s
    }
    private func sectionTitle(_ t: String) -> NSTextField {
        let l = NSTextField(labelWithString: t)
        l.font = NSFont.boldSystemFont(ofSize: 13)
        return l
    }
    private func readOnlyTextView(height: CGFloat) -> NSTextView {
        let tv = NSTextView()
        tv.isEditable = false
        tv.font = NSFont.monospacedSystemFont(ofSize: 10, weight: .regular)
        tv.widthAnchor.constraint(equalToConstant: 420).isActive = true
        tv.heightAnchor.constraint(equalToConstant: height).isActive = true
        return tv
    }

    // MARK: - Live state (2 s timer)

    private func refresh() {
        for p in TCCPermission.allCases {
            permStatus[p]?.stringValue = p.isGranted ? "✓ granted" : "✗ missing"
        }
        grantButton.isHidden = !TCCPermission.allCases.contains { !$0.isGranted }

        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        switch server.state {
        case .running:               serverLabel.stringValue = "running on \(addr)"
        case .starting, .restarting: serverLabel.stringValue = "starting…"
        case .crashed:               serverLabel.stringValue = "crashed"
        case .stopped:               serverLabel.stringValue = "stopped"
        }
        serverButton.title = server.state.isUp ? "Stop server" : "Start server"
        stderrView.isHidden = server.state != .crashed
        if server.state == .crashed { stderrView.string = server.stderrText }
        snippetView.string = Snippet.opencodeEntry(addr: addr, token: token)

        let t = ConfigStore.target(from: json)
        rectLabel.stringValue = (t.mode == "rect" && t.w > 0)
            ? "(\(t.x), \(t.y), \(t.w), \(t.h))" : ""

        // Sync the segment ONLY when the stored mode actually changed, so a
        // user mid-selection in Rectangle mode isn't snapped back every 2 s.
        // Derive the key from the stored mode directly (modeChanged persists
        // the mode immediately, so an unsaved/zero rect never fights the UI).
        let key = t.mode == "rect" ? "rect" : (t.mode == "screen" ? "screen" : "app")
        if key != lastModeKey {
            lastModeKey = key
            segmented.selectedSegment = key == "rect" ? 1 : (key == "screen" ? 2 : 0)
            updateModeRows()
        }

        // Same pattern: sync the access segment only when the stored value
        // actually changed, so an external config edit takes over the UI.
        let acc = ConfigStore.access(from: json)
        let accKey = acc == "read_only" ? "ro" : "full"
        if accKey != lastAccessKey {
            lastAccessKey = accKey
            accessSegmented.selectedSegment = accKey == "ro" ? 1 : 0
            accessCaption.stringValue = accessCaptionText(acc)
        }

        syncScreenPicker()
    }

    private func updateModeRows() {
        pickerRow.isHidden = segmented.selectedSegment != 0
        rectRow.isHidden = segmented.selectedSegment != 1
        screenRow.isHidden = segmented.selectedSegment != 2
    }

    /// Keeps the screen dropdown in sync: rebuilds the items when the display
    /// set changes (fingerprint of sizes), then selects the stored screen.
    /// NSScreen.screens[0] is the main display — the same ordering robotgo
    /// uses (index 0 = main) — so the item index maps straight into config
    /// "screen".
    private func syncScreenPicker() {
        let screens = NSScreen.screens
        let fp = screens.map { "\(Int($0.frame.width))x\(Int($0.frame.height))" }
            .joined(separator: ",")
        if fp != lastScreensFingerprint {
            lastScreensFingerprint = fp
            screenPicker.removeAllItems()
            for (i, s) in screens.enumerated() {
                let title = "Screen \(i + 1) (\(Int(s.frame.width))×\(Int(s.frame.height)))"
                    + (i == 0 ? " (main)" : "")
                screenPicker.addItem(withTitle: title)
                screenPicker.lastItem?.representedObject = i
            }
        }
        let t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        let idx = (t.mode == "screen" && t.screen >= 0 && t.screen < screens.count) ? t.screen : 0
        if screenPicker.indexOfSelectedItem != idx {
            screenPicker.selectItem(at: idx)
        }
    }

    @objc private func accessChanged() {
        let key = accessSegmented.selectedSegment == 1 ? "read_only" : "full"
        guard key != lastAccessKey else { return }
        lastAccessKey = key
        accessCaption.stringValue = accessCaptionText(key)
        do {
            let json = try ConfigStore.load()
            try ConfigStore.save(ConfigStore.applyAccess(key, to: json))
        } catch {
            let a = NSAlert()
            a.messageText = "Config write failed"
            a.informativeText = "\(error)\nThe app will not overwrite config.json — fix it by hand."
            a.runModal()
        }
        refresh()
    }

    private func accessCaptionText(_ access: String) -> String {
        if access == "read_only" {
            return "Read-only: capture, window_info, list_windows, list_screens, doctor, wait — no input, no target switching."
        }
        return "Full access: all 15 tools, including input and target switching."
    }

    @objc private func refreshScreenPicker() {
        lastScreensFingerprint = ""
        syncScreenPicker()
    }

    @objc private func screenPickerChanged() {
        let idx = screenPicker.indexOfSelectedItem
        guard idx >= 0 else { return }
        var t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        t.mode = "screen"
        t.screen = idx
        saveTarget(t)
    }

    // MARK: - Actions

    @objc private func requestPerm(_ sender: NSButton) { TCCPermission.allCases[sender.tag].request() }
    @objc private func grantAll() {
        for p in TCCPermission.allCases where !p.isGranted { p.request() }
    }

    @objc private func modeChanged() {
        let key: String
        switch segmented.selectedSegment {
        case 1: key = "rect"
        case 2: key = "screen"
        default: key = "app"
        }
        guard key != lastModeKey else { return }
        lastModeKey = key
        updateModeRows()
        // T17 defect fix: persist the mode right away. Otherwise the 2 s
        // reload timer sees the stale stored mode, snaps the segment back and
        // hides this row mid-selection (only the rect→rect leg was previously
        // protected via lastModeKey). Keeps every later reload consistent.
        var t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        t.mode = key
        // Defect fix: switching TO App-window mode must adopt the window the
        // picker is currently showing. The stored app can be empty/stale —
        // the rect flow saves app:"" — while the picker still displays the
        // user's previously selected entry, so the mode alone never retargeted
        // it. Placeholder items ("Loading…", "server error: …", "(no windows
        // …)") carry no representedObject, so the stored app is kept there.
        if key == "app",
           let w = picker.selectedItem?.representedObject as? WindowEntry {
            t.app = w.app
            t.title = w.title
        }
        saveTarget(t)
    }

    @objc private func refreshPicker() {
        picker.removeAllItems()
        picker.addItem(withTitle: "Loading…")
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        guard let url = URL(string: "http://\(addr)") else { return }
        let client = MCPClient(baseURL: url, token: token.isEmpty ? nil : token)
        Task { [weak self] in
            do {
                let text = try await client.callToolText("list_windows")
                await MainActor.run {
                    guard let self else { return }
                    self.picker.removeAllItems()
                    var n = 0
                    for line in text.components(separatedBy: "\n") where !line.isEmpty {
                        guard let w = WindowLine.parse(line) else { continue }
                        self.picker.addItem(withTitle: w.title.isEmpty ? w.app : "\(w.app) — \(w.title)")
                        self.picker.lastItem?.representedObject = w
                        n += 1
                    }
                    if n == 0 {
                        self.picker.addItem(withTitle: "(no windows — is the server running?)")
                    } else {
                        self.selectCurrentTargetInPicker()
                    }
                }
            } catch {
                await MainActor.run {
                    self?.picker.removeAllItems()
                    self?.picker.addItem(withTitle: "server error: \(error.localizedDescription)")
                }
            }
        }
    }

    private func selectCurrentTargetInPicker() {
        let t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        guard !t.app.isEmpty else { return }
        // Prefer an exact app+title match — the list can hold several windows
        // of the same app — and fall back to app-only.
        var fallback: NSMenuItem?
        for item in picker.itemArray {
            guard let w = item.representedObject as? WindowEntry else { continue }
            if w.app == t.app && w.title == t.title {
                picker.select(item)
                return
            }
            if fallback == nil && w.app == t.app { fallback = item }
        }
        if let f = fallback { picker.select(f) }
    }

    @objc private func pickerChanged() {
        guard let w = picker.selectedItem?.representedObject as? WindowEntry else { return }
        saveTarget(ConfigStore.Target(app: w.app, title: w.title, mode: "app", x: 0, y: 0, w: 0, h: 0))
    }

    // T16 fix (T15 precedent): retain the overlay — a local `o` would deallocate
    // when selectRect() returns (onResult captures only [weak self]=controller),
    // so onResult would never fire on a real drag.
    private var rectOverlay: RectOverlay?

    @objc private func selectRect() {
        let o = RectOverlay()
        o.onResult = { [weak self] x, y, w, h in
            DispatchQueue.main.async {
                self?.saveTarget(ConfigStore.Target(app: "", title: "", mode: "rect",
                                                    x: x, y: y, w: w, h: h))
            }
        }
        rectOverlay = o
        o.present()
    }

    /// Load-modify-save; on a malformed config, ALERT and touch nothing.
    private func saveTarget(_ t: ConfigStore.Target) {
        do {
            let json = try ConfigStore.load()
            try ConfigStore.save(ConfigStore.applyTarget(t, to: json))
        } catch {
            let a = NSAlert()
            a.messageText = "Config write failed"
            a.informativeText = "\(error)\nThe app will not overwrite config.json — fix it by hand."
            a.runModal()
        }
        refresh()
    }

    @objc private func toggleServer() { server.toggle() }

    @objc private func copySnippet() {
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(Snippet.opencodeEntry(addr: addr, token: token),
                                       forType: .string)
    }
}
