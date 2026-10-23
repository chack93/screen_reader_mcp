import AppKit
import Carbon.HIToolbox
import ScreenReaderKit

/// Owns the app's GLOBAL "stop the server" hotkey (config key `hotkey`) and
/// calls `onFire` when it fires.
///
/// Carbon's RegisterEventHotKey is deprecated since 10.16 but it is still the
/// right tool: it is the only global-key mechanism that needs no TCC grant.
/// NSEvent.addGlobalMonitorForEvents for keyDown and CGEventTap both require
/// Accessibility — and in practice Input Monitoring — which read-only users
/// deliberately do not have. Registrations here are non-exclusive, so a system
/// shortcut that already owns the key keeps working and we simply never fire.
final class HotkeyCenter {
    private let onFire: () -> Void
    /// Retained for the object's lifetime: the handler is a bare C function
    /// pointer, so letting the UPP deallocate would leave Carbon calling into
    /// freed memory.
    private let handlerUPP: EventHandlerUPP
    private var handlerRef: EventHandlerRef?
    private var hotKeyRef: EventHotKeyRef?
    /// The wanted binding, kept even while `suspended` so resume can re-register.
    private var combo: HotkeyCombo?
    /// True while the settings window is recording a new key: the registration
    /// is dropped for that moment, because Carbon swallows a keypress it
    /// recognizes as a hotkey — it never reaches the app as a keyDown, so the
    /// CURRENTLY assigned key could otherwise never be re-recorded (FB7642814).
    private var suspended = false

    /// OSType 'SRMP' identifying our registrations inside EventHotKeyID.
    private static let signature: OSType = 0x5352_4D50
    private static let hotKeyID: UInt32 = 1

    /// Non-capturing, so it converts to the C function pointer Carbon wants.
    private static let handler: EventHandlerUPP = { _, _, userData in
        guard let userData else { return OSStatus(eventNotHandledErr) }
        Unmanaged<HotkeyCenter>.fromOpaque(userData).takeUnretainedValue().fire()
        return noErr
    }

    init(onFire: @escaping () -> Void) {
        self.onFire = onFire
        self.handlerUPP = HotkeyCenter.handler
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                                 eventKind: UInt32(kEventHotKeyPressed))
        let st = withUnsafePointer(to: &spec) {
            InstallEventHandler(GetEventDispatcherTarget(), handlerUPP, 1, $0,
                                Unmanaged.passUnretained(self).toOpaque(), &handlerRef)
        }
        if st != noErr {
            NSLog("screenreader: hotkey handler install failed (OSStatus \(st)); stop hotkey disabled")
        }
    }

    /// (Re)registers `combo`, or drops the registration when nil (hotkey off or
    /// unparseable). Main thread only — Carbon's hot key APIs are documented as
    /// not thread safe.
    func update(_ combo: HotkeyCombo?) {
        self.combo = combo
        if suspended {
            unregister()
            return
        }
        register()
    }

    /// Drops the registration while the recorder listens (`true`), restores it
    /// on `false`. Without this the currently assigned key is eaten by Carbon
    /// and the recorder never sees its keyDown. Main thread only.
    func setSuspended(_ s: Bool) {
        guard s != suspended else { return }
        suspended = s
        s ? unregister() : register()
    }

    private func register() {
        unregister()
        guard let combo, let code = Hotkey.keyCode(combo.key) else { return }
        var ref: EventHotKeyRef?
        let id = EventHotKeyID(signature: Self.signature, id: Self.hotKeyID)
        let st = RegisterEventHotKey(UInt32(code), Self.carbonModifiers(combo), id,
                                     GetEventDispatcherTarget(),
                                     UInt32(kEventHotKeyNoOptions), &ref)
        guard st == noErr else {
            // eventHotKeyExistsErr = another app holds it exclusively.
            NSLog("screenreader: could not register stop hotkey \(Hotkey.format(combo)) (OSStatus \(st))")
            return
        }
        hotKeyRef = ref
    }

    private func unregister() {
        guard let ref = hotKeyRef else { return }
        let st = UnregisterEventHotKey(ref)
        if st != noErr { NSLog("screenreader: hotkey unregister failed (OSStatus \(st))") }
        hotKeyRef = nil
    }

    private static func carbonModifiers(_ c: HotkeyCombo) -> UInt32 {
        var m: UInt32 = 0
        if c.command { m |= UInt32(cmdKey) }
        if c.control { m |= UInt32(controlKey) }
        if c.option { m |= UInt32(optionKey) }
        if c.shift { m |= UInt32(shiftKey) }
        return m
    }

    /// Carbon dispatches on the main event loop already, but the hop is
    /// explicit so the caller's main-thread contract (ServerProcess.stop())
    /// holds no matter how the callback is delivered.
    private func fire() {
        DispatchQueue.main.async { [onFire] in onFire() }
    }

    deinit {
        unregister()
        if let handlerRef {
            let st = RemoveEventHandler(handlerRef)
            if st != noErr { NSLog("screenreader: hotkey handler remove failed (OSStatus \(st))") }
        }
    }
}
