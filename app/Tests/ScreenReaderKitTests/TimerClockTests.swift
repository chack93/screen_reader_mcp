import XCTest
@testable import ScreenReaderKit

final class TimerClockTests: XCTestCase {
    func testFormatsHoursAndMinutes() {
        XCTAssertEqual(TimerClock.format(3 * 3600 + 49 * 60 + 59), "3:49")
        XCTAssertEqual(TimerClock.format(24 * 3600 + 3 * 60), "24:03")
    }

    func testFormatsLessThanOneHour() {
        XCTAssertEqual(TimerClock.format(5 * 60 + 30), "0:05")
        XCTAssertEqual(TimerClock.format(0), "0:00")
        XCTAssertEqual(TimerClock.format(59), "0:00")
        XCTAssertEqual(TimerClock.format(45.9), "0:00")
    }

    func testClampsNegative() {
        XCTAssertEqual(TimerClock.format(-120), "0:00")
    }
}
