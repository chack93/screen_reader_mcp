import ApplicationServices
import CoreGraphics
import Foundation

public enum TCCPermission: String, CaseIterable {
    case screenRecording = "Screen Recording"
    case accessibility = "Accessibility"
    case automation = "Automation (System Events)"

    public var isGranted: Bool {
        switch self {
        case .screenRecording: return CGPreflightScreenCaptureAccess()
        case .accessibility:   return AXIsProcessTrusted()
        case .automation:      return Self.probeAutomation()
        }
    }

    /// Triggers the system prompt for this permission when not yet granted.
    public func request() {
        switch self {
        case .screenRecording:
            _ = CGRequestScreenCaptureAccess()
        case .accessibility:
            let opts = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
            _ = AXIsProcessTrustedWithOptions(opts)
        case .automation:
            _ = Self.probeAutomation() // running it is what surfaces the prompt
        }
    }

    /// Harmless System Events AppleScript — exit 0 only when Automation is granted.
    /// Uses `get name` (a property query) because a literal `return` in a `tell` block
    /// sends no Apple event, so it would never exercise the TCC Automation check.
    static func probeAutomation() -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", "tell application \"System Events\" to get name"]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        do {
            try p.run()
            p.waitUntilExit()
            return p.terminationStatus == 0
        } catch {
            return false
        }
    }
}
