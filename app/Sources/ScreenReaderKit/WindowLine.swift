import Foundation

public struct WindowEntry: Equatable {
    public var app: String
    public var title: String
    public var x: Int
    public var y: Int
    public var w: Int
    public var h: Int
    public init(app: String, title: String, x: Int, y: Int, w: Int, h: Int) {
        self.app = app; self.title = title
        self.x = x; self.y = y; self.w = w; self.h = h
    }
}

public enum WindowLine {
    /// Parses one `list_windows` result line:
    ///   app | title | {X:.. Y:.. W:.. H:..}
    /// The app is the first field and the bounds the last; a title containing
    /// " | " is preserved by rejoining the middle fields.
    public static func parse(_ line: String) -> WindowEntry? {
        let parts = line.components(separatedBy: " | ")
        guard parts.count >= 3 else { return nil }
        let app = parts[0].trimmingCharacters(in: .whitespaces)
        let title = parts[1..<parts.count - 1].joined(separator: " | ")
            .trimmingCharacters(in: .whitespaces)
        let bounds = parts.last!.trimmingCharacters(in: CharacterSet(charactersIn: "{} "))
        var vals: [String: Int] = [:]
        for kv in bounds.split(separator: " ") {
            let p = kv.split(separator: ":")
            guard p.count == 2, let v = Int(p[1]) else { return nil }
            vals[String(p[0])] = v
        }
        guard !app.isEmpty,
              let x = vals["X"], let y = vals["Y"], let w = vals["W"], let h = vals["H"]
        else { return nil }
        return WindowEntry(app: app, title: title, x: x, y: y, w: w, h: h)
    }
}
