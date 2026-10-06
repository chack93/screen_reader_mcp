import XCTest
@testable import ScreenReaderKit

final class KitTests: XCTestCase {
    func testVersion() {
        XCTAssertEqual(Kit.version, "0.1.0")
    }
}
