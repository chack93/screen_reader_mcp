import XCTest
@testable import ScreenReaderKit

final class AccessToggleTests: XCTestCase {
    func testIsReadOnly_onlyFullIsFull() {
        // Mirrors the Go server's tools.Allow: "full" and the empty value are
        // full access; EVERYTHING else (including a garbage value) is enforced
        // as read-only, so the menu must say read-only too.
        XCTAssertFalse(AccessToggle.isReadOnly("full"))
        XCTAssertFalse(AccessToggle.isReadOnly(""))
        XCTAssertTrue(AccessToggle.isReadOnly("read_only"))
        XCTAssertTrue(AccessToggle.isReadOnly("banana"))
    }

    func testNext_flipsBothWays() {
        XCTAssertEqual(AccessToggle.next("full"), "read_only")
        XCTAssertEqual(AccessToggle.next("read_only"), "full")
        XCTAssertEqual(AccessToggle.next(""), "read_only") // empty = full
        // A garbage value is enforced as read-only, so toggling repairs it.
        XCTAssertEqual(AccessToggle.next("banana"), "full")
        // The toggle is its own inverse for the two real modes.
        XCTAssertEqual(AccessToggle.next(AccessToggle.next("full")), "full")
        XCTAssertEqual(AccessToggle.next(AccessToggle.next("read_only")), "read_only")
    }

    func testTitle_showsCurrentModeAndTheAction() {
        // The bare mode name would read as a label, not a button: the title
        // carries the CURRENT mode and what the click does.
        XCTAssertEqual(AccessToggle.title("full"), "Access: full (switch to read-only)")
        XCTAssertEqual(AccessToggle.title("read_only"), "Access: read-only (switch to full)")
        // Display spelling is "read-only"; the config value stays "read_only".
        XCTAssertFalse(AccessToggle.title("read_only").contains("read_only"))
        // Unrecognized value: what the server enforces is read-only.
        XCTAssertEqual(AccessToggle.title("banana"), "Access: read-only (switch to full)")
    }

    func testTitle_followsTheConfigDefault() {
        // An absent config key reads as full, so the menu offers read-only.
        XCTAssertEqual(AccessToggle.title(ConfigStore.access(from: [:])),
                       "Access: full (switch to read-only)")
    }

    func testToggleRoundTrip_preservesOtherKeys() throws {
        let json: [String: Any] = [
            "app": "Sublime Text",
            "mode": "app",
            "capture": ["max_width": 0],
            "hotkey": "off",
        ]
        let flipped = ConfigStore.applyAccess(AccessToggle.next(ConfigStore.access(from: json)), to: json)
        XCTAssertEqual(ConfigStore.access(from: flipped), "read_only")
        XCTAssertEqual(flipped["app"] as? String, "Sublime Text")
        XCTAssertEqual(flipped["mode"] as? String, "app")
        XCTAssertEqual((flipped["capture"] as? [String: Any])?["max_width"] as? Int, 0)
        XCTAssertEqual(flipped["hotkey"] as? String, "off")

        let back = ConfigStore.applyAccess(AccessToggle.next(ConfigStore.access(from: flipped)), to: flipped)
        XCTAssertEqual(ConfigStore.access(from: back), "full")
        XCTAssertEqual(back["app"] as? String, "Sublime Text")
    }
}
