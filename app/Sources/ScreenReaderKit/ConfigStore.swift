import Foundation
import Security

/// Reads/writes ~/.config/screenreader/config.json.
/// Mutates only app/title/mode/rect/http; preserves every other key.
public enum ConfigStore {
    public static let defaultPath: String =
        NSHomeDirectory() + "/.config/screenreader/config.json"

    public struct Target: Equatable {
        public var app: String
        public var title: String
        public var mode: String // "app" | "rect"
        public var x: Int
        public var y: Int
        public var w: Int
        public var h: Int
        public init(app: String, title: String, mode: String, x: Int, y: Int, w: Int, h: Int) {
            self.app = app; self.title = title; self.mode = mode
            self.x = x; self.y = y; self.w = w; self.h = h
        }
    }

    public enum ConfigError: Error { case unparseable, notObject }

    /// Loads the raw JSON object. A missing file returns [:]. Malformed JSON THROWS
    /// (callers must not overwrite a damaged config).
    public static func load(path: String = defaultPath) throws -> [String: Any] {
        guard let data = FileManager.default.contents(atPath: path) else { return [:] }
        let obj: Any
        do { obj = try JSONSerialization.jsonObject(with: data) }
        catch { throw ConfigError.unparseable }
        guard let dict = obj as? [String: Any] else { throw ConfigError.notObject }
        return dict
    }

    /// Atomically rewrites the file (temp + rename, parent dirs created).
    public static func save(_ json: [String: Any], path: String = defaultPath) throws {
        let data: Data
        do { data = try JSONSerialization.data(withJSONObject: json, options: [.prettyPrinted, .sortedKeys]) }
        catch { throw ConfigError.notObject }
        let tmp = path + ".sr-tmp"
        try FileManager.default.createDirectory(
            atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
        do {
            try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
            if FileManager.default.fileExists(atPath: path) {
                try FileManager.default.removeItem(atPath: path)
            }
            try FileManager.default.moveItem(atPath: tmp, toPath: path)
        } catch {
            try? FileManager.default.removeItem(atPath: tmp)
            throw ConfigError.notObject
        }
    }

    public static func applyTarget(_ t: Target, to json: [String: Any]) -> [String: Any] {
        var out = json
        out["app"] = t.app
        out["title"] = t.title
        out["mode"] = t.mode
        out["rect"] = ["x": t.x, "y": t.y, "w": t.w, "h": t.h]
        return out
    }

    public static func target(from json: [String: Any]) -> Target {
        let rect = json["rect"] as? [String: Any] ?? [:]
        func i(_ k: String) -> Int { rect[k] as? Int ?? 0 }
        return Target(app: json["app"] as? String ?? "",
                      title: json["title"] as? String ?? "",
                      mode: json["mode"] as? String ?? "app",
                      x: i("x"), y: i("y"), w: i("w"), h: i("h"))
    }

    public static func httpConfig(from json: [String: Any]) -> (addr: String, token: String) {
        let http = json["http"] as? [String: Any] ?? [:]
        return (http["addr"] as? String ?? "127.0.0.1:18427",
                http["token"] as? String ?? "")
    }

    /// Sets http.token only when missing/empty; preserves everything else.
    public static func ensureToken(_ token: String, in json: [String: Any]) -> [String: Any] {
        var out = json
        var http = out["http"] as? [String: Any] ?? [:]
        if (http["token"] as? String).map({ $0.isEmpty }) ?? true {
            http["token"] = token
            out["http"] = http
        }
        return out
    }

    /// 32 random bytes, lower-case hex (64 chars).
    public static func generateToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
