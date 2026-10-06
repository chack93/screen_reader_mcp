import Foundation

public enum RectMath {
    public struct IPoint: Equatable {
        public var x: Int
        public var y: Int
        public init(x: Int, y: Int) { self.x = x; self.y = y }
    }

    /// Two drag corners (any order) → (x, y, w, h) with positive size.
    public static func normalize(_ a: IPoint, _ b: IPoint) -> (x: Int, y: Int, w: Int, h: Int) {
        (x: min(a.x, b.x), y: min(a.y, b.y), w: abs(b.x - a.x), h: abs(b.y - a.y))
    }

    /// Maps a point from a FLIPPED full-screen overlay view (origin top-left of
    /// that screen, Y down) to screen-absolute top-left-origin (Quartz) coords
    /// — the space the Go engine uses.
    ///   appkitY = screenMinY + screenHeight - viewY   (flipped → AppKit global)
    ///   quartzY = globalTop - appkitY                 (globalTop = max frame.maxY)
    public static func toScreenAbsolute(viewX: Int, viewY: Int,
                                        screenMinX: CGFloat, screenMinY: CGFloat,
                                        screenHeight: CGFloat, globalTop: CGFloat) -> (x: Int, y: Int) {
        let appkitX = screenMinX + CGFloat(viewX)
        let appkitY = screenMinY + screenHeight - CGFloat(viewY)
        return (x: Int(appkitX.rounded()), y: Int((globalTop - appkitY).rounded()))
    }
}
