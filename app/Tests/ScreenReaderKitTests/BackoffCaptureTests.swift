import AppKit // only to synthesize PNG fixtures (NSBitmapImageRep)
import XCTest
@testable import ScreenReaderKit

final class BackoffCaptureTests: XCTestCase {
    func testBackoffSequence() {
        XCTAssertEqual(Backoff.delay(attempt: 0), 1)
        XCTAssertEqual(Backoff.delay(attempt: 1), 2)
        XCTAssertEqual(Backoff.delay(attempt: 2), 4)
        XCTAssertEqual(Backoff.delay(attempt: 4), 16)
        XCTAssertEqual(Backoff.delay(attempt: 5), 30) // capped
        XCTAssertEqual(Backoff.delay(attempt: -1), 1) // clamped low
    }

    private func png(_ gray: CGFloat) -> Data {
        let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 4, pixelsHigh: 4,
                                   bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false,
                                   isPlanar: false, colorSpaceName: .deviceRGB,
                                   bytesPerRow: 0, bitsPerPixel: 0)!
        for y in 0..<4 { for x in 0..<4 {
            // T10 host finding: NSColor(white:) (Generic Gray) and other non-device
            // colors silently write BLACK via setColor here; deviceRGB is reliable.
            rep.setColor(NSColor(deviceRed: gray, green: gray, blue: gray, alpha: 1), atX: x, y: y)
        } }
        return rep.representation(using: .png, properties: [:])!
    }

    func testIsAllBlack() {
        XCTAssertTrue(CaptureCheck.isAllBlack(png(0)))
        XCTAssertFalse(CaptureCheck.isAllBlack(png(1)))
    }

    func testIsAllBlackMixed() {
        let black = NSColor(deviceRed: 0, green: 0, blue: 0, alpha: 1)
        let white = NSColor(deviceRed: 1, green: 1, blue: 1, alpha: 1) // see T10 host finding above
        let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 4, pixelsHigh: 4,
                                   bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false,
                                   isPlanar: false, colorSpaceName: .deviceRGB,
                                   bytesPerRow: 0, bitsPerPixel: 0)!
        for y in 0..<4 { for x in 0..<4 { rep.setColor(black, atX: x, y: y) } }
        rep.setColor(white, atX: 2, y: 2)
        XCTAssertFalse(CaptureCheck.isAllBlack(rep.representation(using: .png, properties: [:])!))
    }

    func testUndecodableIsNotAllBlack() {
        XCTAssertFalse(CaptureCheck.isAllBlack(Data("garbage".utf8)))
    }
}
