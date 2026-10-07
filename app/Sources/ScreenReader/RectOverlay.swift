import AppKit
import ScreenReaderKit

/// Transparent full-screen overlay (one borderless window per display). Drag
/// to draw a rect; on release it is delivered in screen-absolute, top-left
/// origin, Y-down coords (the engine's space). Esc cancels silently; a
/// near-zero drag delivers nothing.
final class RectOverlay {
    var onResult: ((Int, Int, Int, Int) -> Void)?
    private var windows: [NSWindow] = []

    private final class OverlayWindow: NSWindow {
        override var canBecomeKey: Bool { true } // borderless windows can't be key by default
    }

    func present() {
        dismiss()
        let globalTop = NSScreen.screens.map { $0.frame.maxY }.max() ?? 0
        for screen in NSScreen.screens {
            let win = OverlayWindow(contentRect: screen.frame, styleMask: [.borderless],
                                    backing: .buffered, defer: false)
            win.level = .screenSaver
            win.backgroundColor = NSColor.black.withAlphaComponent(0.2)
            win.isOpaque = false
            win.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            let v = DragView(frame: NSRect(origin: .zero, size: screen.frame.size))
            let (minX, minY, height) = (screen.frame.minX, screen.frame.minY, screen.frame.height)
            v.onFinish = { [weak self] a, b in
                guard let self else { return }
                self.dismiss()
                let pa = RectMath.toScreenAbsolute(viewX: a.x, viewY: a.y,
                                                   screenMinX: minX, screenMinY: minY,
                                                   screenHeight: height, globalTop: globalTop)
                let pb = RectMath.toScreenAbsolute(viewX: b.x, viewY: b.y,
                                                   screenMinX: minX, screenMinY: minY,
                                                   screenHeight: height, globalTop: globalTop)
                let x = min(pa.x, pb.x), y = min(pa.y, pb.y)
                let w = abs(pb.x - pa.x), h = abs(pb.y - pa.y)
                if w >= 2 && h >= 2 { self.onResult?(x, y, w, h) }
            }
            win.contentView = v
            win.makeKeyAndOrderFront(nil)
            windows.append(win)
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    private func dismiss() {
        for w in windows { w.orderOut(nil) }
        windows.removeAll()
    }

    /// Draws in its OWN flipped coordinates: origin top-left of this screen,
    /// Y down. Conversion to global space happens once, in present().
    private final class DragView: NSView {
        var onFinish: ((RectMath.IPoint, RectMath.IPoint) -> Void)?
        private var a: RectMath.IPoint?
        private var b: RectMath.IPoint?

        override var isFlipped: Bool { true }
        override var acceptsFirstResponder: Bool { true }

        override func mouseDown(with e: NSEvent) {
            let p = convert(e.locationInWindow, from: nil)
            a = RectMath.IPoint(x: Int(p.x), y: Int(p.y)); b = a
            needsDisplay = true
        }
        override func mouseDragged(with e: NSEvent) {
            guard a != nil else { return }
            let p = convert(e.locationInWindow, from: nil)
            b = RectMath.IPoint(x: Int(p.x), y: Int(p.y))
            needsDisplay = true
        }
        override func mouseUp(with e: NSEvent) {
            let (sa, sb) = (a, b); a = nil; b = nil
            if let sa, let sb { onFinish?(sa, sb) }
        }
        override func keyDown(with e: NSEvent) {
            if e.keyCode == 53 { onFinish = nil; window?.orderOut(nil) } // Esc cancels
        }
        override func draw(_ dirtyRect: NSRect) {
            guard let a, let b else { return }
            let n = RectMath.normalize(a, b)
            let path = NSBezierPath(rect: NSRect(x: n.x, y: n.y, width: n.w, height: n.h))
            NSColor.white.setStroke()
            path.lineWidth = 2
            path.stroke()
        }
    }
}
