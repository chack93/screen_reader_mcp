import Foundation
import XCTest
@testable import ScreenReaderKit

private final class StubURLProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) -> (Int, [String: String], Data))?
    nonisolated(unsafe) static var requests: [URLRequest] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var req = request
        if req.httpBody == nil, let stream = req.httpBodyStream {
            // URLSession may move the body into a stream before URLProtocol sees it.
            stream.open()
            var data = Data()
            let bufferSize = 4096
            let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            while stream.hasBytesAvailable {
                let read = stream.read(buffer, maxLength: bufferSize)
                if read <= 0 { break }
                data.append(buffer, count: read)
            }
            buffer.deallocate()
            stream.close()
            req.httpBody = data
        }
        Self.requests.append(req)
        let (status, headers, body) = Self.handler!(req)
        var h = headers
        if h["Content-Type"] == nil { h["Content-Type"] = "application/json" }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status,
                                   httpVersion: "HTTP/1.1", headerFields: h)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        if !body.isEmpty { client?.urlProtocol(self, didLoad: body) }
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

final class MCPClientTests: XCTestCase {
    var session: URLSession!

    override func setUp() {
        StubURLProtocol.requests = []
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [StubURLProtocol.self]
        session = URLSession(configuration: cfg)
    }

    func testInitializeThenCallToolText() async throws {
        StubURLProtocol.handler = { req in
            let body = req.httpBody.flatMap { String(data: $0, encoding: .utf8) } ?? ""
            if body.contains("\"initialize\"") {
                return (200, ["mcp-session-id": "S1"],
                        Data(#"{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"screenreader","version":"0.1.0"}}}"#.utf8))
            }
            if body.contains("notifications/initialized") {
                return (202, [:], Data())
            }
            if body.contains("\"tools/call\"") {
                return (200, [:],
                        Data(#"{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello from server"}]}}"#.utf8))
            }
            return (500, [:], Data(#"{"error":"unexpected"}"#.utf8))
        }

        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!,
                           token: "tok", session: session)
        let text = try await c.callToolText("list_windows")
        XCTAssertEqual(text, "hello from server")

        // session + auth headers carried on the tools/call
        let call = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(call.value(forHTTPHeaderField: "Mcp-Session-Id"), "S1")
        XCTAssertEqual(call.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
    }

    func testSSEResponseIsParsed() async throws {
        StubURLProtocol.handler = { req in
            let body = req.httpBody.flatMap { String(data: $0, encoding: .utf8) } ?? ""
            if body.contains("\"initialize\"") {
                return (200, ["mcp-session-id": "S1"],
                        Data(#"{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"screenreader","version":"0.1.0"}}}"#.utf8))
            }
            if body.contains("notifications/initialized") { return (202, [:], Data()) }
            return (200, ["Content-Type": "text/event-stream"],
                    Data("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"sse ok\"}]}}\n\n".utf8))
        }
        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!, session: session)
        let text = try await c.callToolText("doctor")
        XCTAssertEqual(text, "sse ok")
    }

    func testUnauthorizedThrows() async {
        StubURLProtocol.handler = { _ in (401, [:], Data("nope".utf8)) }
        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!, session: session)
        do {
            _ = try await c.callToolText("capture")
            XCTFail("expected unauthorized")
        } catch let e as MCPClientError {
            guard case .unauthorized = e else { return XCTFail("wrong error: \(e)") }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }
}
