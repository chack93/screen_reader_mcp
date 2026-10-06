import Foundation

public enum MCPClientError: Error {
    case unauthorized
    case server(Int)
    case transport(String)
}

/// Minimal MCP client over Streamable HTTP: initialize handshake + tools/call.
/// The server is configured for JSON responses; SSE `data:` frames are tolerated.
public final class MCPClient: @unchecked Sendable {
    private let baseURL: URL
    private let token: String?
    private let session: URLSession
    private let lock = NSLock()
    private var sessionId: String?
    private var ready = false

    public init(baseURL: URL, token: String? = nil,
                session: URLSession = URLSession(configuration: .ephemeral)) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    /// Calls a tool and returns the full `result` JSON object.
    public func callTool(_ name: String, arguments: [String: Any] = [:]) async throws -> [String: Any] {
        try await ensureReady()
        var params: [String: Any] = ["name": name]
        if !arguments.isEmpty { params["arguments"] = arguments }
        let msg = try await send(id: 3, method: "tools/call", params: params)
        if let err = msg["error"] as? [String: Any] {
            throw MCPClientError.transport("\(err["message"] ?? "tool error")")
        }
        guard let result = msg["result"] as? [String: Any] else {
            throw MCPClientError.transport("no result in response")
        }
        return result
    }

    /// Calls a tool and returns its text content joined.
    public func callToolText(_ name: String, arguments: [String: Any] = [:]) async throws -> String {
        let result = try await callTool(name, arguments: arguments)
        let content = result["content"] as? [[String: Any]] ?? []
        let text = content.compactMap { item in
            (item["type"] as? String) == "text" ? (item["text"] as? String) : nil
        }.joined(separator: "\n")
        return text
    }

    private func ensureReady() async throws {
        lock.lock()
        let was = ready
        lock.unlock()
        guard !was else { return }
        _ = try await send(id: 1, method: "initialize", params: [
            "protocolVersion": "2025-06-18",
            "capabilities": [String: Any](),
            "clientInfo": ["name": "screenreader-app", "version": "0.1.0"],
        ])
        try await send(method: "notifications/initialized", params: [String: Any]())
        lock.lock()
        ready = true
        lock.unlock()
    }

    @discardableResult
    private func send(id: Int? = nil, method: String, params: [String: Any]) async throws -> [String: Any] {
        var body: [String: Any] = ["jsonrpc": "2.0", "method": method, "params": params]
        if let id { body["id"] = id }
        guard let data = try? JSONSerialization.data(withJSONObject: body,
                                                     options: [.withoutEscapingSlashes]) else {
            throw MCPClientError.transport("encode failed")
        }
        var req = URLRequest(url: baseURL.appendingPathComponent("mcp"))
        req.httpMethod = "POST"
        req.timeoutInterval = 10
        req.httpBody = data
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        lock.lock()
        if let sid = sessionId { req.setValue(sid, forHTTPHeaderField: "Mcp-Session-Id") }
        lock.unlock()

        let (raw, resp): (Data, URLResponse)
        do { (raw, resp) = try await session.data(for: req) }
        catch { throw MCPClientError.transport(error.localizedDescription) }
        guard let http = resp as? HTTPURLResponse else {
            throw MCPClientError.transport("no HTTP response")
        }
        switch http.statusCode {
        case 200, 202: break
        case 401: throw MCPClientError.unauthorized
        default: throw MCPClientError.server(http.statusCode)
        }
        if let sid = http.value(forHTTPHeaderField: "Mcp-Session-Id") ??
            http.value(forHTTPHeaderField: "mcp-session-id") {
            lock.lock(); sessionId = sid; lock.unlock()
        }
        guard !raw.isEmpty else { return [:] }
        // JSON body, or SSE frames — take the last data: line that carries an id.
        let ct = http.value(forHTTPHeaderField: "Content-Type") ?? ""
        var payload = raw
        if ct.contains("text/event-stream") {
            let s = String(decoding: raw, as: UTF8.self)
            for line in s.components(separatedBy: "\n") where line.hasPrefix("data:") {
                let json = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                if json.contains("\"id\"") { payload = Data(json.utf8) }
            }
        }
        guard let obj = try? JSONSerialization.jsonObject(with: payload),
              let dict = obj as? [String: Any] else {
            throw MCPClientError.transport("unparseable response")
        }
        return dict
    }
}
