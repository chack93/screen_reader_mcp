import CoreGraphics
import Foundation

/// Adds a small filled "recording-style" dot badge at the TOP-RIGHT of an
/// icon, in display orientation (top-left origin, Y-down — the project
/// convention). The dot is separated from the glyph by a 1-pt clear ring so
/// it reads as a badge even when the icon edge is near.
///
/// Coverage is computed analytically per pixel (center-inside-circle), not
/// via CG path fills: with anti-aliasing off, fillEllipse covers a pixel
/// when ANY part of it intersects the path (punching the whole bounding
/// square), and the AA-on path rasterization of a small ellipse is not
/// pixel-predictable. A pixel loop is exact and the tests pin it down.
/// Runs once at launch on a small icon — cost is negligible.
public enum MenuBadge {
    /// Returns a copy of `base` with a dot `dotDiameter` pts across,
    /// `inset` pts from the top-right corner. Same pixel size as `base`.
    public static func withDot(_ base: CGImage, dotDiameter: CGFloat = 4, inset: CGFloat = 1) -> CGImage {
        let w = base.width, h = base.height
        let space = CGColorSpace(name: CGColorSpace.sRGB)!
        guard let src = CGContext(
            data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
            space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
            return base
        }
        src.draw(base, in: CGRect(x: 0, y: 0, width: w, height: h))
        guard let baseBytes = src.data?.assumingMemoryBound(to: UInt8.self) else { return base }

        guard let dst = CGContext(
            data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
            space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
            return base
        }
        guard let out = dst.data?.assumingMemoryBound(to: UInt8.self) else { return base }

        // Dot center in display coordinates (y-down, pixel centers at +0.5).
        let radius = dotDiameter / 2
        let cx = CGFloat(w) - inset - radius
        let cy = inset + radius
        let ring = radius + 1

        for y in 0..<h {
            for x in 0..<w {
                let i = (y * w + x) * 4
                let dx = CGFloat(x) + 0.5 - cx
                let dy = CGFloat(y) + 0.5 - cy
                let d = (dx * dx + dy * dy).squareRoot()
                if d <= radius {
                    out[i] = 0; out[i + 1] = 0; out[i + 2] = 0; out[i + 3] = 255 // template ink
                } else if d <= ring {
                    out[i] = 0; out[i + 1] = 0; out[i + 2] = 0; out[i + 3] = 0 // punched ring
                } else {
                    out[i] = baseBytes[i]
                    out[i + 1] = baseBytes[i + 1]
                    out[i + 2] = baseBytes[i + 2]
                    out[i + 3] = baseBytes[i + 3]
                }
            }
        }
        return dst.makeImage() ?? base
    }
}
