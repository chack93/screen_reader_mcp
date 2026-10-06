import Foundation

public enum Snippet {
    private struct Server: Encodable {
        let type: String
        let url: String
        let oauth: Bool
        let headers: [String: String]?
    }
    private struct Entry: Encodable {
        struct MCP: Encodable { let servers: [String: Server] }
        let mcp: MCP
    }

    /// The opencode.json server entry (a fragment to merge under mcp.servers)
    /// for this loopback endpoint.
    public static func opencodeEntry(addr: String, token: String) -> String {
        let entry = Entry(mcp: .init(servers: ["screenreader": .init(
            type: "remote",
            url: "http://\(addr)/mcp",
            oauth: false,
            headers: token.isEmpty ? nil : ["Authorization": "Bearer \(token)"],
        )]))
        let enc = JSONEncoder()
        // withoutEscapingSlashes: keeps the URL readable in the copy-pasteable
        // snippet (JSONSerialization would emit "http:\/\/…").
        enc.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? enc.encode(entry),
              let s = String(data: data, encoding: .utf8) else { return "{}" }
        return s
    }
}
