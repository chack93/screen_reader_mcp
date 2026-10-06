import CoreGraphics
import Foundation
import ImageIO

public enum CaptureCheck {
    /// True when `pngData` decodes and EVERY pixel is near-black (all channels
    /// ≤ threshold). The TCC canary: a capture without an effective Screen
    /// Recording grant comes back fully black. Undecodable data is NOT
    /// all-black (that's a different failure).
    public static func isAllBlack(_ pngData: Data, threshold: UInt8 = 8) -> Bool {
        guard let src = CGImageSourceCreateWithData(pngData as CFData, nil),
              let img = CGImageSourceCreateImageAtIndex(src, 0, nil),
              img.width > 0, img.height > 0 else { return false }
        // T10 finding: `CGContext(data: &bytes, …)` aliases an unreliable buffer
        // (the context writes to a different storage than the Swift array reads), so
        // use an explicitly allocated buffer we own, draw into it, then copy out.
        let count = img.width * img.height * 4
        let buf = UnsafeMutableRawPointer.allocate(byteCount: count, alignment: 16)
        defer { buf.deallocate() }
        guard let ctx = CGContext(data: buf, width: img.width, height: img.height,
                                  bitsPerComponent: 8, bytesPerRow: img.width * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        else { return false }
        ctx.draw(img, in: CGRect(x: 0, y: 0, width: img.width, height: img.height))
        let bytes = Array(UnsafeBufferPointer(start: buf.assumingMemoryBound(to: UInt8.self), count: count))
        for i in stride(from: 0, to: bytes.count, by: 4) {
            if bytes[i] > threshold || bytes[i + 1] > threshold || bytes[i + 2] > threshold {
                return false
            }
        }
        return true
    }
}
