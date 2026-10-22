import CoreGraphics
import Foundation
import XCTest
@testable import ScreenReaderKit

/// Renders a CGImage into an RGBA8 buffer and hands back a pixel accessor in
/// DISPLAY orientation (y grows DOWN from the top-left — the project
/// convention). A CG bitmap context's buffer row 0 is the TOP scanline, so
/// display y maps straight onto the buffer row.
private func pixels(_ img: CGImage) -> (w: Int, h: Int, px: (Int, Int) -> (UInt8, UInt8, UInt8, UInt8)) {
    let w = img.width, h = img.height
    var bytes = [UInt8](repeating: 0, count: w * h * 4)
    let ctx = CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8,
                        bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: w, height: h))
    return (w, h, { x, y in
        let i = (y * w + x) * 4
        return (bytes[i], bytes[i + 1], bytes[i + 2], bytes[i + 3])
    })
}

/// Fully opaque base with a known per-pixel pattern (R=x, G=y-display, B=100)
/// so "untouched" is checkable pixel-by-pixel, not just uniformity.
private func patternBase(w: Int = 20, h: Int = 20) -> CGImage {
    var bytes = [UInt8](repeating: 0, count: w * h * 4)
    for y in 0..<h {
        for x in 0..<w {
            let i = (y * w + x) * 4 // buffer row 0 = top scanline (display y)
            bytes[i] = UInt8(x)
            bytes[i + 1] = UInt8(y)
            bytes[i + 2] = 100
            bytes[i + 3] = 255
        }
    }
    return CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8,
                     bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                     bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!.makeImage()!
}

/// With the defaults (dotDiameter 4, inset 1) on a 20x20 image the dot
/// center is at display (17, 3): 1 pt from the top-right corner plus half
/// the dot. Coverage is decided at the PIXEL CENTER (x+0.5, y+0.5) — the
/// same model the production pixel loop uses.
private let dotCenter = (x: 17, y: 3)
private func dist(_ x: Int, _ y: Int) -> CGFloat {
    let dx = CGFloat(x) + 0.5 - CGFloat(dotCenter.x)
    let dy = CGFloat(y) + 0.5 - CGFloat(dotCenter.y)
    return (dx * dx + dy * dy).squareRoot()
}

final class MenuBadgeTests: XCTestCase {
    func testDotIsOpaqueAtTopRight() {
        let out = pixels(MenuBadge.withDot(patternBase()))
        var checked = 0
        for y in 0..<out.h {
            for x in 0..<out.w where dist(x, y) < 1.9 {
                let (r, g, b, a) = out.px(x, y)
                XCTAssertEqual(a, 255, "dot pixel (\(x),\(y)) must be fully opaque")
                // Solid template ink (0,0,0) — NOT the base pattern (r==x,
                // g==y): a wrongly positioned dot would leave the pattern.
                if (r, g, b) != (0, 0, 0) {
                    XCTFail("dot pixel (\(x),\(y)) must be ink, not base pattern: got (\(r),\(g),\(b))")
                }
                checked += 1
            }
        }
        XCTAssertGreaterThanOrEqual(checked, 9, "expected the 9 interior dot pixels, got \(checked)")
    }

    func testRingBandIsPunchedClear() {
        let base = pixels(patternBase())
        let out = pixels(MenuBadge.withDot(patternBase()))
        // The analytic punch is exact: the 1-pt band (radius 2..3) is fully
        // clear. Pixel-center distances in that band: 2.915 (seven lattice
        // pixels).
        var checked = 0
        for y in 0..<out.h {
            for x in 0..<out.w where dist(x, y) > 2.7 && dist(x, y) < 3.4 {
                let (_, _, _, aBase) = base.px(x, y)
                XCTAssertEqual(aBase, 255, "test precondition: base is opaque at (\(x),\(y))")
                let (_, _, _, a) = out.px(x, y)
                XCTAssertEqual(a, 0, "ring pixel (\(x),\(y)) must be punched clear, got \(a)")
                checked += 1
            }
        }
        XCTAssertGreaterThanOrEqual(checked, 4, "expected ring pixels on the lattice, got \(checked)")
    }

    func testPixelsOutsideBadgeUntouched() {
        let base = pixels(patternBase())
        let out = pixels(MenuBadge.withDot(patternBase()))
        XCTAssertEqual(out.w, 20)
        XCTAssertEqual(out.h, 20)
        for y in 0..<out.h {
            for x in 0..<out.w where dist(x, y) > 3.75 {
                let e = base.px(x, y)
                let o = out.px(x, y)
                if o != e {
                    XCTFail("pixel (\(x),\(y)) outside the badge must be untouched: got \(o), want \(e)")
                }
            }
        }
    }
}
