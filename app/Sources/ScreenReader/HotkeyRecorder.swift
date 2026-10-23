import AppKit
import ScreenReaderKit

/// Field-style recorder for the stop hotkey, modelled on the shortcut fields
/// macOS itself uses in System Settings: a bordered, text-field-like box that
/// displays the current binding and records the next combo on click. It never
/// accepts free text — its label is only ever set programmatically from
/// `Hotkey`'s grammar. (The current HIG documents no dedicated recorder
/// control, so the system's own shortcut field is the pattern to follow; the
/// old click-to-record push button read as an action, not as a shortcut.)
///
/// Click it, press the combo (any key from `Hotkey`'s table, with any of
/// ⌃⌥⇧⌘), and `onChange` fires with the new binding. Esc, or a click anywhere
/// else, cancels.
///
/// It listens with a LOCAL event monitor: while recording, this window is key
/// and the events already belong to this app, so no Accessibility/Input
/// Monitoring grant is involved — that grant is only needed to watch *other*
/// apps' keys, which is what `HotkeyCenter` gets from Carbon instead. A local
/// monitor also sees ⌘-combos before menu key-equivalents are processed, so
/// recording ⌘-bindings works.
final class HotkeyRecorder: NSView {
    /// Fires with the recorded combo; the controller persists it.
    var onChange: ((HotkeyCombo) -> Void)?
    /// Fires true/false when recording starts/stops. The app uses it to
    /// suspend the global Carbon registration while listening: Carbon
    /// swallows keypresses it recognizes as hotkeys (they never arrive as
    /// keyDown), so the currently assigned key could not be re-recorded.
    var onRecordingChange: ((Bool) -> Void)?
    private(set) var isRecording = false

    private var monitor: Any?
    private var labelIdle = "Off"
    private var startedAt = Date.distantPast

    private let text: NSTextField = {
        let t = NSTextField(labelWithString: "Off")
        t.alignment = .center
        t.font = NSFont.systemFont(ofSize: 11.5)
        t.lineBreakMode = .byTruncatingTail
        t.usesSingleLineMode = true
        t.cell?.truncatesLastVisibleLine = true
        t.translatesAutoresizingMaskIntoConstraints = false
        return t
    }()

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        addSubview(text)
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: 160),
            heightAnchor.constraint(equalToConstant: 22),
            text.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            text.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            text.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { fatalError("not used") }
    deinit { stopRecording() }

    /// Shows the stored spec (config `hotkey`) without changing it. A spec that
    /// is neither disabled nor parseable is shown verbatim, so a hand-edited typo
    /// stays visible in the UI instead of masquerading as "Off".
    func setBinding(_ spec: String) {
        if let combo = Hotkey.parse(spec) {
            labelIdle = Hotkey.glyphs(combo)
        } else if Hotkey.isDisabled(spec) {
            labelIdle = "Off"
        } else {
            labelIdle = spec
        }
        if !isRecording { text.stringValue = labelIdle }
    }

    /// Cancels recording (the controller calls this when the window closes).
    func cancel() { stopRecording() }

    // MARK: - Field appearance

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5),
                                xRadius: 4, yRadius: 4)
        NSColor.textBackgroundColor.setFill()
        path.fill()
        (isRecording ? NSColor.controlAccentColor : NSColor.separatorColor).setStroke()
        path.lineWidth = isRecording ? 1.5 : 1
        path.stroke()
    }

    override func resetCursorRects() { addCursorRect(bounds, cursor: .pointingHand) }

    // MARK: - Recording

    override var acceptsFirstResponder: Bool { true }
    /// A click must start recording even when the window is not yet key —
    /// otherwise the first click only activates the app.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) { toggleRecording() }

    /// Full Keyboard Access: Tab can reach the field, Return/Space start
    /// recording — mirroring how a push button activates.
    override func keyDown(with event: NSEvent) {
        if !isRecording, event.keyCode == 36 || event.keyCode == 49 { toggleRecording() }
        else { super.keyDown(with: event) }
    }

    private func toggleRecording() {
        isRecording ? stopRecording() : startRecording()
    }

    private func startRecording() {
        guard !isRecording else { return }
        window?.makeKey()
        NSApp.activate(ignoringOtherApps: true)
        isRecording = true
        startedAt = Date()
        text.stringValue = "Press keys…"
        needsDisplay = true
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .leftMouseDown, .systemDefined]) { [weak self] e in
            guard let self, self.isRecording else { return e }
            return self.handle(e)
        }
        onRecordingChange?(true)
    }

    private func stopRecording() {
        guard isRecording || monitor != nil else { return }
        let wasRecording = isRecording
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        isRecording = false
        text.stringValue = labelIdle   // never leave a transient hint on the field
        needsDisplay = true
        if wasRecording { onRecordingChange?(false) }
    }

    /// Returning nil swallows the event, so recording ⌘Q cannot quit the app
    /// and Esc cannot close the window mid-recording.
    private func handle(_ e: NSEvent) -> NSEvent? {
        switch e.type {
        case .leftMouseDown:
            guard !bounds.contains(convert(e.locationInWindow, from: nil)) else {
                return e          // our own click → toggleRecording cancels
            }
            stopRecording()
            return e
        case .systemDefined:
            // A media-mapped F-key never arrives as keyDown, so a bare F9 on a
            // Mac with "Use F1, F2 … as standard function keys" off would look
            // dead — say what happened instead. But ONLY for a genuine media
            // key that arrives after recording settles: recent macOS also emits
            // payload-free systemDefined events as a byproduct of the click
            // that opened the recorder, and reacting to those flashed "media
            // key" on every click. `isMediaKeyEvent` rejects the payload-free
            // phantom; the timing floor rejects any click-timed genuine-looking
            // event (no real keypress can land within 0.3 s of the click).
            // Anything else is passed through so it cannot be mistaken for a
            // media key while recording stays live on "Press keys…".
            if Hotkey.isMediaKeyEvent(subtype: Int(e.subtype.rawValue), data1: e.data1),
               Date().timeIntervalSince(startedAt) > 0.3 {
                text.stringValue = "media key — try Fn or ⌃/⌥"
                return nil
            }
            return e
        case .keyDown:
            text.stringValue = "Press keys…"
            if e.keyCode == UInt16(Hotkey.keyCode("esc") ?? 53) {
                stopRecording()
                return nil
            }
            let f = e.modifierFlags.intersection(.deviceIndependentFlagsMask)
            guard let combo = Hotkey.combo(code: e.keyCode,
                                           command: f.contains(.command),
                                           control: f.contains(.control),
                                           option: f.contains(.option),
                                           shift: f.contains(.shift)) else {
                text.stringValue = "key not supported"
                return nil
            }
            setBinding(Hotkey.format(combo))
            stopRecording()
            onChange?(combo)
            return nil
        default:
            return e
        }
    }
}
