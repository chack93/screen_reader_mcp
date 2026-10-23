import XCTest
@testable import ScreenReaderKit

final class HotkeyTests: XCTestCase {
    func testDefaultIsF9() {
        XCTAssertEqual(Hotkey.defaultSpec, "f9")
        XCTAssertEqual(Hotkey.parse("f9"), HotkeyCombo(key: "f9"))
        XCTAssertEqual(Hotkey.keyCode("f9"), 101) // kVK_F9
    }

    // MARK: parse — modifiers

    func testParseSingleModifier() {
        XCTAssertEqual(Hotkey.parse("ctrl+f9"), HotkeyCombo(key: "f9", control: true))
        XCTAssertEqual(Hotkey.parse("opt+f9"), HotkeyCombo(key: "f9", option: true))
        XCTAssertEqual(Hotkey.parse("shift+f9"), HotkeyCombo(key: "f9", shift: true))
        XCTAssertEqual(Hotkey.parse("cmd+f9"), HotkeyCombo(key: "f9", command: true))
    }

    func testParseModifierAliases() {
        // Same alias table as the server's key tool (keycombo.ParseCombo).
        for spec in ["control+f9", "ctrl+f9"] {
            XCTAssertEqual(Hotkey.parse(spec), HotkeyCombo(key: "f9", control: true), spec)
        }
        for spec in ["option+f9", "alt+f9", "opt+f9"] {
            XCTAssertEqual(Hotkey.parse(spec), HotkeyCombo(key: "f9", option: true), spec)
        }
        for spec in ["command+f9", "cmd+f9"] {
            XCTAssertEqual(Hotkey.parse(spec), HotkeyCombo(key: "f9", command: true), spec)
        }
    }

    func testParseMultipleModifiers() {
        XCTAssertEqual(Hotkey.parse("ctrl+opt+f9"),
                       HotkeyCombo(key: "f9", control: true, option: true))
        XCTAssertEqual(Hotkey.parse("cmd+ctrl+opt+shift+k"),
                       HotkeyCombo(key: "k", command: true, control: true, option: true, shift: true))
    }

    func testParseIsCaseAndSpaceTolerant() {
        XCTAssertEqual(Hotkey.parse("  CTRL + F9 "), HotkeyCombo(key: "f9", control: true))
        XCTAssertEqual(Hotkey.parse("Cmd+Shift+A"), HotkeyCombo(key: "a", command: true, shift: true))
    }

    func testParseDedupesRepeatedModifier() {
        XCTAssertEqual(Hotkey.parse("ctrl+ctrl+f9"), HotkeyCombo(key: "f9", control: true))
    }

    // MARK: parse — rejections

    func testParseRejectsGarbage() {
        XCTAssertNil(Hotkey.parse("hyper+f9"))     // unknown modifier
        XCTAssertNil(Hotkey.parse("f13"))          // outside the supported table
        XCTAssertNil(Hotkey.parse("ctrl"))         // modifier as the key
        XCTAssertNil(Hotkey.parse("f9+ctrl"))      // key must be last
        XCTAssertNil(Hotkey.parse("cmd++f9"))      // empty token
        XCTAssertNil(Hotkey.parse("cmd+"))
        XCTAssertNil(Hotkey.parse("  "))
        XCTAssertNil(Hotkey.parse("§"))            // not in the table
    }

    func testParseDisabledValues() {
        for spec in ["", "off", "none", "disabled", "OFF", "  "] {
            XCTAssertTrue(Hotkey.isDisabled(spec), spec)
            XCTAssertNil(Hotkey.parse(spec), spec)
        }
        XCTAssertFalse(Hotkey.isDisabled("f9"))
    }

    // MARK: format

    func testFormatCanonicalOrder() {
        // Modifiers always print in ⌃⌥⇧⌘ order, key last.
        XCTAssertEqual(Hotkey.format(HotkeyCombo(key: "f9")), "f9")
        XCTAssertEqual(Hotkey.format(HotkeyCombo(key: "f9", command: true)), "cmd+f9")
        XCTAssertEqual(Hotkey.format(HotkeyCombo(key: "f9", control: true, option: true)), "ctrl+opt+f9")
        XCTAssertEqual(Hotkey.format(HotkeyCombo(key: "k", command: true, control: true,
                                                 option: true, shift: true)),
                       "ctrl+opt+shift+cmd+k")
    }

    func testParseFormatRoundTrip() {
        for spec in ["f9", "ctrl+f9", "opt+f12", "cmd+shift+a", "ctrl+opt+shift+cmd+3"] {
            guard let c = Hotkey.parse(spec) else { return XCTFail("parse \(spec) failed") }
            XCTAssertEqual(Hotkey.parse(Hotkey.format(c)), c, spec)
        }
        // Aliases and casing normalize on the way out.
        XCTAssertEqual(Hotkey.parse("ALT + F12").flatMap { Hotkey.format($0) }, "opt+f12")
    }

    // MARK: glyphs (menu + recorder label)

    func testGlyphs() {
        XCTAssertEqual(Hotkey.glyphs(HotkeyCombo(key: "f9")), "F9")
        XCTAssertEqual(Hotkey.glyphs(HotkeyCombo(key: "f9", control: true)), "⌃F9")
        XCTAssertEqual(Hotkey.glyphs(HotkeyCombo(key: "a", command: true, shift: true)), "⇧⌘A")
        XCTAssertEqual(Hotkey.glyphs(HotkeyCombo(key: "space", control: true, option: true)), "⌃⌥Space")
    }

    // MARK: key code table (verified against the SDK's Events.h)

    func testFunctionKeyCodes() {
        let f: [String: UInt16] = ["f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96,
                                   "f6": 97, "f7": 98, "f8": 100, "f9": 101, "f10": 109,
                                   "f11": 103, "f12": 111]
        for (name, code) in f {
            XCTAssertEqual(Hotkey.keyCode(name), code, name)
            XCTAssertEqual(Hotkey.keyName(code), name, "\(code)")   // reverse lookup, for the recorder
        }
    }

    func testLetterDigitAndNamedKeyCodes() {
        XCTAssertEqual(Hotkey.keyCode("a"), 0)
        XCTAssertEqual(Hotkey.keyCode("s"), 1)
        XCTAssertEqual(Hotkey.keyCode("k"), 40)
        XCTAssertEqual(Hotkey.keyCode("z"), 6)
        XCTAssertEqual(Hotkey.keyCode("1"), 18)
        XCTAssertEqual(Hotkey.keyCode("5"), 23)
        XCTAssertEqual(Hotkey.keyCode("0"), 29)
        XCTAssertEqual(Hotkey.keyCode("space"), 49)
        XCTAssertEqual(Hotkey.keyCode("return"), 36)
        XCTAssertEqual(Hotkey.keyCode("tab"), 48)
        XCTAssertEqual(Hotkey.keyCode("esc"), 53)
        XCTAssertNil(Hotkey.keyCode("f13"))
        XCTAssertNil(Hotkey.keyName(77))
    }

    func testEveryParseableKeyHasACodeAndBack() {
        let names = ["f1", "f5", "f9", "f12", "a", "m", "z", "0", "7", "space", "return", "tab", "esc"]
        for n in names {
            guard let code = Hotkey.keyCode(n) else { return XCTFail("no code for \(n)") }
            XCTAssertEqual(Hotkey.keyName(code), n, "\(n)")
            // What the recorder builds from a captured (code, modifiers) pair.
            XCTAssertEqual(Hotkey.combo(code: code, command: false, control: true,
                                        option: false, shift: false),
                           HotkeyCombo(key: n, control: true), n)
        }
        XCTAssertNil(Hotkey.combo(code: 77, command: false, control: false,
                                  option: false, shift: false))
    }

    // MARK: media-key systemDefined events (recorder hint gate)

    func testIsMediaKeyEvent() {
        // Genuine function-mapped / aux media keys: subtype 8 or 11, non-zero payload.
        XCTAssertTrue(Hotkey.isMediaKeyEvent(subtype: 8, data1: 0x0a00_0001))
        XCTAssertTrue(Hotkey.isMediaKeyEvent(subtype: 11, data1: 1))
        // Click/activation byproducts: other subtype, or a zero payload.
        XCTAssertFalse(Hotkey.isMediaKeyEvent(subtype: 0, data1: 0))    // the phantom click event
        XCTAssertFalse(Hotkey.isMediaKeyEvent(subtype: 1, data1: 0))    // fn modifier record
        XCTAssertFalse(Hotkey.isMediaKeyEvent(subtype: 8, data1: 0))    // right subtype, no payload
    }
}
