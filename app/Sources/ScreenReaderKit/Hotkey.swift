import Foundation

/// One global-hotkey binding: a key name plus the modifiers it requires.
public struct HotkeyCombo: Equatable {
    /// Lower-case key name from `Hotkey.keyCodes` ("f9", "k", "space").
    public var key: String
    public var command: Bool
    public var control: Bool
    public var option: Bool
    public var shift: Bool

    public init(key: String, command: Bool = false, control: Bool = false,
                option: Bool = false, shift: Bool = false) {
        self.key = key
        self.command = command
        self.control = control
        self.option = option
        self.shift = shift
    }
}

/// The app's "stop the server" hotkey spec (config key `hotkey`): parses and
/// formats `"f9"` / `"ctrl+opt+f9"` and maps key names to Carbon key codes.
///
/// Pure, so the grammar and the code table are unit-testable; the Carbon
/// registration itself lives in the app target (HotkeyCenter). The grammar is
/// the same "+"-separated form the server's `key` tool takes, with the same
/// modifier aliases (cmd/command, ctrl/control, opt/option/alt, shift) and the
/// key last — see server/internal/keycombo.
public enum Hotkey {
    /// Config default, matching the TODO's "by default F9".
    public static let defaultSpec = "f9"

    /// Specs that mean "no hotkey at all" (the hotkey can be turned off).
    static let disabledValues: Set<String> = ["", "off", "none", "disabled"]

    /// True when `spec` means the hotkey is switched off.
    public static func isDisabled(_ spec: String) -> Bool {
        disabledValues.contains(normalize(spec))
    }

    /// Parses a spec; nil when it is disabled or not a usable combo.
    public static func parse(_ spec: String) -> HotkeyCombo? {
        let s = normalize(spec)
        if isDisabled(s) { return nil }
        // Keep empty tokens so "cmd++f9" and "cmd+" are rejected, not skipped.
        let parts = s.split(separator: "+", omittingEmptySubsequences: false)
            .map { normalize(String($0)) }
        guard let key = parts.last, keyCodes[key] != nil else { return nil }
        var combo = HotkeyCombo(key: key)
        for token in parts.dropLast() {
            switch token {
            case "cmd", "command": combo.command = true
            case "ctrl", "control": combo.control = true
            case "opt", "option", "alt": combo.option = true
            case "shift": combo.shift = true
            default: return nil
            }
        }
        return combo
    }

    /// Canonical spec: modifiers in ⌃⌥⇧⌘ order, key last ("ctrl+opt+f9").
    public static func format(_ combo: HotkeyCombo) -> String {
        var parts: [String] = []
        if combo.control { parts.append("ctrl") }
        if combo.option { parts.append("opt") }
        if combo.shift { parts.append("shift") }
        if combo.command { parts.append("cmd") }
        parts.append(combo.key)
        return parts.joined(separator: "+")
    }

    /// Menu/recorder label in macOS glyph order: "⌃⌥F9", "⇧⌘A", "F9".
    public static func glyphs(_ combo: HotkeyCombo) -> String {
        var s = ""
        if combo.control { s += "⌃" }
        if combo.option { s += "⌥" }
        if combo.shift { s += "⇧" }
        if combo.command { s += "⌘" }
        switch combo.key {
        case "space": return s + "Space"
        case "return": return s + "↩"
        case "tab": return s + "⇥"
        default: return s + combo.key.uppercased()
        }
    }

    /// Builds a combo from a captured key code plus modifier flags; nil when the
    /// code is outside the table (the recorder refuses those keys).
    public static func combo(code: UInt16, command: Bool, control: Bool,
                             option: Bool, shift: Bool) -> HotkeyCombo? {
        guard let name = keyName(code) else { return nil }
        return HotkeyCombo(key: name, command: command, control: control,
                           option: option, shift: shift)
    }

    public static func keyCode(_ name: String) -> UInt16? { keyCodes[normalize(name)] }

    public static func keyName(_ code: UInt16) -> String? { namesByCode[code] }

    /// True when a captured `.systemDefined` event carries a real media/function
    /// key payload: the hidd subtypes are 8 (function-mapped keys — the shape a
    /// media-mapped F-key has when "Use F1, F2 … as standard function keys" is
    /// off) and 11 (aux control buttons: volume/illumination), and both always
    /// encode the key in a non-zero `data1`. Recent macOS also emits
    /// payload-free systemDefined events as click/activation byproducts; those
    /// must not be mistaken for media keys (they made the recorder flash its
    /// "media key" hint on every click).
    public static func isMediaKeyEvent(subtype: Int, data1: Int) -> Bool {
        (subtype == 8 || subtype == 11) && data1 != 0
    }

    private static func normalize(_ s: String) -> String {
        s.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    /// Supported keys: the function keys, letters, digits and a few named keys.
    /// Codes are Carbon kVK_* values taken from the SDK's
    /// HIToolbox/Events.h (ANSI layout). "esc" is parseable but not recordable —
    /// the recorder uses Esc to cancel.
    static let keyCodes: [String: UInt16] = [
        "f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
        "f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
        "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
        "c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16,
        "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23,
        "9": 25, "7": 26, "8": 28, "0": 29, "o": 31, "u": 32, "i": 34,
        "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46,
        "return": 36, "tab": 48, "space": 49, "esc": 53,
    ]

    static let namesByCode: [UInt16: String] =
        Dictionary(uniqueKeysWithValues: keyCodes.map { ($1, $0) })
}
