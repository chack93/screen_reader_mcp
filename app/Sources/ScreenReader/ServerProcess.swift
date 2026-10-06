import AppKit
import Foundation
import ScreenReaderKit

/// T13 fix: child pid for the SIGTERM handler. Written on q in spawn()/killChild().
/// SIGTERM (killall / force-quit) never runs applicationWillTerminate, so the
/// handler kills the child directly. The Go server does not trap SIGTERM.
/// NOTE (deviation from plan): UnsafeAtomic lives in Synchronization (macOS 15+);
/// this package targets macOS 13, so a plain file-scope Int32 is used — an
/// aligned 32-bit load/store is already lock-free on arm64 and global literal
/// init is static, so the signal handler never races lazy initialization.
private var sigChildPid: Int32 = 0

/// Spawns and supervises the embedded Go server (`screenreader --http`):
/// SIGTERM on quit, auto-restart with backoff after crashes, /health polling
/// for state. All mutable process bookkeeping lives on one serial queue.
final class ServerProcess {
    enum State: Equatable { case stopped, starting, running, restarting, crashed }

    var onStateChange: ((State) -> Void)?
    private(set) var state: State = .stopped

    var binaryURL: URL? { Bundle.main.resourceURL?.appendingPathComponent("screenreader") }

    private var proc: Process?
    private var errTail = ""
    private var attempts = 0
    private var stopping = false
    private var healthTimer: Timer?
    private let q = DispatchQueue(label: "screenreader.server")

    init() { signal(SIGTERM, handleSigterm) }

    private var addr: String {
        ((try? ConfigStore.load()).map { ConfigStore.httpConfig(from: $0).addr })
            ?? "127.0.0.1:18427"
    }

    var stderrText: String { q.sync { errTail } }

    /// Clean (re)launch: kills any lingering child and spawns fresh.
    func start() {
        q.async {
            self.stopping = false
            self.attempts = 0
            self.killStaleListener() // T13 fix: sweep port orphans (SIGKILL etc.)
            self.killChild()
            self.spawn()
        }
    }

    /// T13 fix: kill any leftover process still LISTENing on our port (SIGKILL
    /// orphans, prior crashes) so the new child can bind. No-op when free.
    private func killStaleListener() {
        guard let colon = addr.lastIndex(of: ":") else { return }
        let port = String(addr[addr.index(after: colon)...])
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/lsof")
        p.arguments = ["-ti", "tcp:\(port)", "-sTCP:LISTEN"]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        do {
            try p.run()
            p.waitUntilExit()
            let out = String(decoding: pipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            for line in out.split(whereSeparator: \.isNewline) {
                if let pid = Int32(line.trimmingCharacters(in: .whitespaces)) { kill(pid, SIGTERM) }
            }
        } catch {}
    }

    func restart() { start() }

    func stop() {
        DispatchQueue.main.async { self.healthTimer?.invalidate(); self.healthTimer = nil }
        q.async {
            self.stopping = true
            self.killChild()
            self.set(.stopped)
        }
    }

    private func spawn() {
        guard let bin = binaryURL, FileManager.default.isExecutableFile(atPath: bin.path) else {
            errTail = "embedded Go binary not found — run build.sh"
            set(.crashed)
            return
        }
        set(attempts == 0 ? .starting : .restarting)
        let p = Process()
        p.executableURL = bin
        p.arguments = ["--http"]
        p.standardOutput = FileHandle.nullDevice
        let pipe = Pipe()
        p.standardError = pipe
        pipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self, !d.isEmpty else { return }
            self.q.async {
                self.errTail += String(decoding: d, as: UTF8.self)
                if self.errTail.count > 4000 { self.errTail = String(self.errTail.suffix(4000)) }
            }
        }
        p.terminationHandler = { [weak self] done in
            self?.q.async {
                // Ignore exits of replaced children (restart/stop) — only the
                // CURRENT child crashing unexpectedly triggers the backoff.
                guard let self, !self.stopping, self.proc === done else { return }
                self.proc = nil
                self.handleExit()
            }
        }
        do {
            try p.run()
            proc = p
            sigChildPid = p.processIdentifier // T13 fix
            startHealth()
        } catch {
            errTail = "spawn failed: \(error.localizedDescription)"
            set(.crashed)
        }
    }

    private func handleExit() {
        guard attempts < 3 else { set(.crashed); return }
        let d = Backoff.delay(attempt: attempts)
        attempts += 1
        set(.restarting)
        q.asyncAfter(deadline: .now() + d) { [weak self] in
            guard let self, !self.stopping else { return }
            self.spawn()
        }
    }

    /// Must run on q. Detaches the child FIRST so its termination handler is
    /// recognized as stale and does not trigger a backoff restart.
    private func killChild() {
        guard let p = proc else { return }
        proc = nil
        sigChildPid = 0 // T13 fix
        if p.isRunning {
            p.terminate() // SIGTERM
            let deadline = Date().addingTimeInterval(3)
            while p.isRunning && Date() < deadline { usleep(50_000) }
            if p.isRunning { kill(p.processIdentifier, SIGKILL) }
        }
    }

    private func startHealth() {
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.healthTimer?.invalidate()
            let t = Timer(timeInterval: 2, repeats: true) { [weak self] _ in self?.checkHealth() }
            RunLoop.main.add(t, forMode: .common)
            self.healthTimer = t
            self.checkHealth()
        }
    }

    private func checkHealth() {
        guard let url = URL(string: "http://\(addr)/health") else { return }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        URLSession.shared.dataTask(with: req) { [weak self] data, resp, _ in
            guard let self else { return }
            let ok = (resp as? HTTPURLResponse)?.statusCode == 200 && data != nil
            if ok { self.q.async { self.attempts = 0; self.set(.running) } }
        }.resume()
    }

    private func set(_ s: State) {
        DispatchQueue.main.async {
            self.state = s
            self.onStateChange?(s)
        }
    }
}

/// T13 fix: SIGTERM (killall / force-quit) does not run applicationWillTerminate.
/// No captures → converts to a C function pointer; body is async-signal-safe
/// (lock-free 32-bit load + kill(2) + _exit(3)).
private func handleSigterm(_ sig: Int32) {
    let pid = sigChildPid
    if pid > 0 { kill(pid, SIGTERM) }
    _exit(143)
}
