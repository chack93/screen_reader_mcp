import XCTest
@testable import ScreenReaderKit

final class SnippetWindowLineTests: XCTestCase {
    func testSnippetWithToken() throws {
        let s = Snippet.opencodeEntry(addr: "127.0.0.1:18427", token: "abc")
        XCTAssertTrue(s.contains("http://127.0.0.1:18427/mcp"))
        XCTAssertTrue(s.contains("Bearer abc"))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any])
        let mcp = try XCTUnwrap(obj["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        let sr = try XCTUnwrap(servers["screenreader"] as? [String: Any])
        XCTAssertEqual(sr["type"] as? String, "remote")
        XCTAssertEqual(sr["url"] as? String, "http://127.0.0.1:18427/mcp")
        XCTAssertEqual(sr["oauth"] as? Bool, false)
        let headers = try XCTUnwrap(sr["headers"] as? [String: Any])
        XCTAssertEqual(headers["Authorization"] as? String, "Bearer abc")
    }

    func testSnippetWithoutTokenOmitsHeaders() throws {
        let s = Snippet.opencodeEntry(addr: "127.0.0.1:1", token: "")
        XCTAssertFalse(s.contains("Bearer"))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any])
        let servers = try XCTUnwrap((obj["mcp"] as? [String: Any])?["servers"] as? [String: Any])
        let sr = try XCTUnwrap(servers["screenreader"] as? [String: Any])
        XCTAssertNil(sr["headers"])
    }

    func testWindowLineParse() {
        let w = WindowLine.parse("Microsoft Remote Desktop | 10.0.0.5 | {X:100 Y:50 W:800 H:600}")
        XCTAssertNotNil(w)
        XCTAssertEqual(w?.app, "Microsoft Remote Desktop")
        XCTAssertEqual(w?.title, "10.0.0.5")
        XCTAssertEqual(w?.x, 100); XCTAssertEqual(w?.y, 50)
        XCTAssertEqual(w?.w, 800); XCTAssertEqual(w?.h, 600)
    }

    func testWindowLineTitleWithPipes() {
        // app is the first field, bounds the last; a title containing " | " survives
        let w = WindowLine.parse("Finder | a | b | {X:0 Y:0 W:1 H:1}")
        XCTAssertEqual(w?.app, "Finder")
        XCTAssertEqual(w?.title, "a | b")
    }

    func testWindowLineRejectsBadInput() {
        XCTAssertNil(WindowLine.parse("no delimiters here"))
        XCTAssertNil(WindowLine.parse("app | title | notabounds"))
        XCTAssertNil(WindowLine.parse(""))
    }
}
