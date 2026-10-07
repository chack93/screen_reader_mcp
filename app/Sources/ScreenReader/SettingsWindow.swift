import AppKit
import ScreenReaderKit

final class SettingsWindowController: NSWindowController, NSWindowDelegate {
    private let server: ServerProcess
    private var permStatus: [TCCPermission: NSTextField] = [:]
    private var grantButton: NSButton!
    private var segmented: NSSegmentedControl!
    private var pickerRow: NSStackView!
    private var rectRow: NSStackView!
    private var picker: NSPopUpButton!
    private var rectLabel: NSTextField!
    private var serverLabel: NSTextField!
    private var snippetView: NSTextView!
    private var stderrView: NSTextView!
    private var lastModeKey = ""
    private var timer: Timer?

    init(server: ServerProcess) {
        self.server = server
        let win = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 620),
                           styleMask: [.titled, .closable], backing: .buffered, defer: false)
        win.title = "ScreenReader"
        win.center()
        super.init(window: win)
        win.delegate = self   // T16 fix: stop the refresh timer when the window closes
        build()
        refresh()
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
        target.addArrangedSubview(sectionTitle("Target"))
        segmented = NSSegmentedControl(labels: ["App window", "Rectangle"],
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

        target.addArrangedSubview(pickerRow)
        target.addArrangedSubview(rectRow)
        root.addArrangedSubview(target)

        // --- Server ---
        let srv = stack(.vertical)
        srv.addArrangedSubview(sectionTitle("Server"))
        let srvRow = stack(.horizontal)
        serverLabel = NSTextField(labelWithString: "…")
        srvRow.addArrangedSubview(serverLabel)
        srvRow.addArrangedSubview(NSView())
        srvRow.addArrangedSubview(NSButton(title: "Restart", target: self,
                                           action: #selector(restartServer)))
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
        stderrView.isHidden = server.state != .crashed
        if server.state == .crashed { stderrView.string = server.stderrText }
        snippetView.string = Snippet.opencodeEntry(addr: addr, token: token)

        let t = ConfigStore.target(from: json)
        rectLabel.stringValue = (t.mode == "rect" && t.w > 0)
            ? "(\(t.x), \(t.y), \(t.w), \(t.h))" : ""

        // Sync the segment ONLY when the stored mode actually changed, so a
        // user mid-selection in Rectangle mode isn't snapped back every 2 s.
        let key = (t.mode == "rect" && t.w > 0) ? "rect" : "app"
        if key != lastModeKey {
            lastModeKey = key
            segmented.selectedSegment = key == "rect" ? 1 : 0
            updateModeRows()
        }
    }

    private func updateModeRows() {
        pickerRow.isHidden = segmented.selectedSegment != 0
        rectRow.isHidden = segmented.selectedSegment != 1
    }

    // MARK: - Actions

    @objc private func requestPerm(_ sender: NSButton) { TCCPermission.allCases[sender.tag].request() }
    @objc private func grantAll() {
        for p in TCCPermission.allCases where !p.isGranted { p.request() }
    }

    @objc private func modeChanged() {
        lastModeKey = segmented.selectedSegment == 1 ? "rect" : "app"
        updateModeRows()
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
        for item in picker.itemArray {
            if let w = item.representedObject as? WindowEntry, w.app == t.app {
                picker.select(item)
                return
            }
        }
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

    @objc private func restartServer() { server.restart() }

    @objc private func copySnippet() {
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(Snippet.opencodeEntry(addr: addr, token: token),
                                       forType: .string)
    }
}
