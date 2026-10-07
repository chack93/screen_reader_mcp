import Foundation
import os

public enum MCPClientError: Error {
    case unauthorized
    case server(Int)
    case transport(String)
}

/// Minimal MCP client over Streamable HTTP: initialize handshake + tools/call.
/// The server is configured for JSON responses; SSE `data:` frames are tolerated.
public final class MCPClient: @unchecked Sendable {
    private struct State {
        var sessionId: String?
        var ready = false
    }

    private let baseURL: URL
    private let token: String?
    private let session: URLSession
    // OSAllocatedUnfairLock instead of NSLock: lock()/unlock() are unavailable
    // from async contexts (Swift 6 error); withLock is a synchronous scoped
    // critical section and this is exactly the short, non-suspending use.
    private let state = OSAllocatedUnfairLock(initialState: State())

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
        let was = state.withLock { $0.ready }
        guard !was else { return }
        _ = try await send(id: 1, method: "initialize", params: [
            "protocolVersion": "2025-06-18",
            "capabilities": [String: Any](),
            "clientInfo": ["name": "screenreader-app", "version": "0.1.0"],
        ])
        try await send(method: "notifications/initialized", params: [String: Any]())
        state.withLock { $0.ready = true }
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
        if let sid = state.withLock({ $0.sessionId }) { req.setValue(sid, forHTTPHeaderField: "Mcp-Session-Id") }

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
            state.withLock { $0.sessionId = sid }
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
