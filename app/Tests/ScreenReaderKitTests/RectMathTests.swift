import XCTest
@testable import ScreenReaderKit

final class RectMathTests: XCTestCase {
    func testNormalizeBothDragDirections() {
        let a = RectMath.IPoint(x: 100, y: 50), b = RectMath.IPoint(x: 200, y: 150)
        XCTAssertEqual(RectMath.normalize(a, b).x, 100)
        XCTAssertEqual(RectMath.normalize(a, b).y, 50)
        XCTAssertEqual(RectMath.normalize(a, b).w, 100)
        XCTAssertEqual(RectMath.normalize(a, b).h, 100)
        // reversed drag → same rect
        XCTAssert(RectMath.normalize(b, a) == RectMath.normalize(a, b))
        // zero-size drag collapses
        XCTAssertEqual(RectMath.normalize(a, a).w, 0)
    }

    func testToScreenAbsolute_mainDisplay() {
        // main display: AppKit frame (0,0,1440,900) ⇒ globalTop = 900
        func map(_ vx: Int, _ vy: Int) -> (x: Int, y: Int) {
            RectMath.toScreenAbsolute(viewX: vx, viewY: vy,
                                       screenMinX: 0, screenMinY: 0, screenHeight: 900, globalTop: 900)
        }
        XCTAssertEqual(map(0, 0).x, 0);        XCTAssertEqual(map(0, 0).y, 0)   // top-left
        XCTAssertEqual(map(100, 40).x, 100);   XCTAssertEqual(map(100, 40).y, 40)
        XCTAssertEqual(map(1439, 899).x, 1439); XCTAssertEqual(map(1439, 899).y, 899) // bottom-right
    }

    func testToScreenAbsolute_secondaryBelowMain() {
        // secondary screen right + below main: AppKit frame (1440, -1080, 1920, 1080), globalTop = 900
        let r = RectMath.toScreenAbsolute(viewX: 0, viewY: 0,
                                          screenMinX: 1440, screenMinY: -1080, screenHeight: 1080, globalTop: 900)
        // view top-left = AppKit y = -1080+1080 = 0 ⇒ Quartz y = 900 - 0 = 900
        XCTAssertEqual(r.x, 1440); XCTAssertEqual(r.y, 900)
    }
}
