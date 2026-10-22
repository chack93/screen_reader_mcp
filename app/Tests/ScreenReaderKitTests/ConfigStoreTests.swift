import XCTest
@testable import ScreenReaderKit

final class ConfigStoreTests: XCTestCase {
    func tmpConfig() -> String {
        NSTemporaryDirectory() + "sr-cfg-\(UUID().uuidString).json"
    }

    func testLoad_missingReturnsEmpty() throws {
        XCTAssertTrue(try ConfigStore.load(path: "/nonexistent/xyz.json").isEmpty)
    }

    func testLoad_unparseableThrows() throws {
        let p = tmpConfig()
        try "not json {{{".write(toFile: p, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(atPath: p) }
        XCTAssertThrowsError(try ConfigStore.load(path: p))
    }

    func testApplyTarget_preservesOtherKeys() {
        let json: [String: Any] = [
            "version": 1,
            "capture": ["max_width": 1600],
            "input": ["delay_ms": 120],
            "app": "OldApp",
        ]
        let out = ConfigStore.applyTarget(
            ConfigStore.Target(app: "Finder", title: "Files", mode: "app", x: 0, y: 0, w: 0, h: 0),
            to: json)
        XCTAssertEqual(out["app"] as? String, "Finder")
        XCTAssertEqual(out["title"] as? String, "Files")
        XCTAssertEqual(out["mode"] as? String, "app")
        XCTAssertEqual(out["version"] as? Int, 1)
        let capture = out["capture"] as? [String: Any]
        XCTAssertEqual(capture?["max_width"] as? Int, 1600)
        let input = out["input"] as? [String: Any]
        XCTAssertEqual(input?["delay_ms"] as? Int, 120)
    }

    func testTargetRoundTrip() {
        let t = ConfigStore.Target(app: "A", title: "T", mode: "rect", x: 10, y: 20, w: 300, h: 200)
        XCTAssertEqual(ConfigStore.target(from: ConfigStore.applyTarget(t, to: [:])), t)
    }

    func testTargetRoundTrip_screenMode() {
        let t = ConfigStore.Target(app: "", title: "", mode: "screen", x: 0, y: 0, w: 0, h: 0,
                                   screen: 1)
        let back = ConfigStore.target(from: ConfigStore.applyTarget(t, to: [:]))
        XCTAssertEqual(back, t)
        XCTAssertEqual(back.screen, 1)
    }

    func testTarget_missingScreenKeyDefaultsToZero() {
        let back = ConfigStore.target(from: ["mode": "screen"])
        XCTAssertEqual(back.screen, 0)
        XCTAssertEqual(back.mode, "screen")
    }

    func testAccessDefaultsToFull() {
        XCTAssertEqual(ConfigStore.access(from: [:]), "full")
        XCTAssertEqual(ConfigStore.access(from: ["access": "read_only"]), "read_only")
    }

    func testApplyAccess_preservesOtherKeys() {
        let json: [String: Any] = [
            "app": "A",
            "capture": ["max_width": 1600],
        ]
        let out = ConfigStore.applyAccess("read_only", to: json)
        XCTAssertEqual(out["access"] as? String, "read_only")
        XCTAssertEqual(out["app"] as? String, "A")
        XCTAssertEqual((out["capture"] as? [String: Any])?["max_width"] as? Int, 1600)
        // round-trip
        XCTAssertEqual(ConfigStore.access(from: ConfigStore.applyAccess("full", to: [:])), "full")
    }

    func testHTTPConfigDefaults() {
        XCTAssertEqual(ConfigStore.httpConfig(from: [:]).addr, "127.0.0.1:18427")
        XCTAssertEqual(ConfigStore.httpConfig(from: [:]).token, "")
        let j = ["http": ["addr": "127.0.0.1:1", "token": "t"]]
        XCTAssertEqual(ConfigStore.httpConfig(from: j).addr, "127.0.0.1:1")
        XCTAssertEqual(ConfigStore.httpConfig(from: j).token, "t")
    }

    func testEnsureToken() {
        let set = ConfigStore.ensureToken("tok", in: ["app": "A"])
        XCTAssertEqual((set["http"] as? [String: Any])?["token"] as? String, "tok")
        XCTAssertEqual(set["app"] as? String, "A")
        // already present → untouched
        let keep = ConfigStore.ensureToken("new", in: ["http": ["token": "old"]])
        XCTAssertEqual((keep["http"] as? [String: Any])?["token"] as? String, "old")
    }

    func testSaveLoadRoundTrip() throws {
        let p = tmpConfig()
        defer { try? FileManager.default.removeItem(atPath: p) }
        var json = ConfigStore.applyTarget(
            ConfigStore.Target(app: "X", title: "", mode: "app", x: 0, y: 0, w: 0, h: 0), to: [:])
        json["keep"] = ["nested": true]
        try ConfigStore.save(json, path: p)
        let back = try ConfigStore.load(path: p)
        XCTAssertEqual(back["app"] as? String, "X")
        XCTAssertEqual((back["keep"] as? [String: Any])?["nested"] as? Bool, true)
    }

    func testSaveOverwritesExistingAtomically() throws {
        let p = tmpConfig()
        defer { try? FileManager.default.removeItem(atPath: p) }
        try ConfigStore.save(["app": "First", "keep": 1], path: p)
        // A crash between remove and rename used to lose the file; the POSIX
        // rename must replace the existing config in one step.
        try ConfigStore.save(["app": "Second"], path: p)
        let back = try ConfigStore.load(path: p)
        XCTAssertEqual(back["app"] as? String, "Second")
        XCTAssertNil(back["keep"])
        XCTAssertFalse(FileManager.default.fileExists(atPath: p + ".sr-tmp"))
    }

    func testSaveCreatesParentDirs() throws {
        let p = NSTemporaryDirectory() + "sr-nested-\(UUID().uuidString)/a/b/config.json"
        defer { try? FileManager.default.removeItem(atPath: p) }
        try ConfigStore.save(["version": 1], path: p)
        XCTAssertEqual(try ConfigStore.load(path: p)["version"] as? Int, 1)
    }

    func testGenerateToken() {
        let t = ConfigStore.generateToken()
        XCTAssertEqual(t.count, 64)
        XCTAssertTrue(t.allSatisfy { "0123456789abcdef".contains($0) })
        XCTAssertNotEqual(t, ConfigStore.generateToken())
    }
}
